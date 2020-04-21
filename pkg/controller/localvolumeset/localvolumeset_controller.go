package localvolumeset

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/util"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	LocalVolumeSetNameLabel      = "local.storage.openshift.io/localvolumeset-owner-name"
	LocalVolumeSetNamespaceLabel = "local.storage.openshift.io/localvolumeset-owner-namespace"
)

var log = logf.Log.WithName("controller_localvolumeset")

// Add creates a new LocalVolumeSet Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileLocalVolumeSet{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("localvolumeset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource PersistentVolume and requeue the LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(obj handler.MapObject) []reconcile.Request {
			LVSName, isLVSNamePresent := obj.Meta.GetLabels()[LocalVolumeSetNameLabel]
			LVSNamespace, isLVSNamespacePresent := obj.Meta.GetLabels()[LocalVolumeSetNamespaceLabel]
			if isLVSNamePresent && isLVSNamespacePresent {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: LVSName, Namespace: LVSNamespace}}}
			}
			return []reconcile.Request{}
		}),
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileLocalVolumeSet implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileLocalVolumeSet{}

// ReconcileLocalVolumeSet reconciles a LocalVolumeSet object
type ReconcileLocalVolumeSet struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	reqLogger logr.Logger
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileLocalVolumeSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.reqLogger = log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	r.reqLogger.Info("Reconciling LocalVolumeSet")
	// Fetch the LocalVolumeSet instance
	instance := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	err = r.syncRBACPolicies(instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.syncLocalVolumeSetDaemon(instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.syncProvisionerConfigMap(instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.syncProvisionerDaemonset(instance, r.getProvisionerConfigMapName(instance))
	if err != nil {
		return reconcile.Result{}, err
	}

	localPVs := &corev1.PersistentVolumeList{}
	err = r.client.List(context.TODO(), localPVs,
		&client.ListOptions{
			LabelSelector: labels.SelectorFromSet(labels.Set{
				LocalVolumeSetNameLabel:      instance.GetName(),
				LocalVolumeSetNamespaceLabel: instance.GetNamespace(),
			}),
		})
	if err != nil {
		return reconcile.Result{}, err
	}
	totalPVCount := int32(len(localPVs.Items))
	instance.Status.TotalProvisionedDeviceCount = &totalPVCount
	instance.Status.ObservedGeneration = instance.Generation
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileLocalVolumeSet) syncLocalVolumeSetDaemon(cr *localv1alpha1.LocalVolumeSet) error {
	ds := newDiscoveryDaemonsetForCR(cr)
	oldDs := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: ds.Name, Namespace: ds.Namespace}}
	_, err := controllerutil.CreateOrUpdate(context.TODO(), r.client, oldDs, func() error {
		if oldDs.Labels == nil {
			oldDs.Labels = make(map[string]string)
		}
		for k, v := range ds.Labels {
			oldDs.Labels[k] = v
		}
		oldDs.Spec = ds.Spec
		return controllerutil.SetControllerReference(cr, oldDs, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileLocalVolumeSet) applyNodeSelector(cr *localv1alpha1.LocalVolumeSet, ds *appsv1.DaemonSet) *appsv1.DaemonSet {
	nodeSelector := cr.Spec.NodeSelector
	if nodeSelector != nil {
		ds.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: nodeSelector,
			},
		}
	}
	return ds
}

// newDiscDaemonsetForCR returns a busybox pod with the same name/namespace as the cr
func newDiscoveryDaemonsetForCR(cr *localv1alpha1.LocalVolumeSet) *appsv1.DaemonSet {
	labels := map[string]string{
		"app": cr.Name,
	}
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	volumes := []corev1.Volume{

		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: util.GetLocalDiskLocationPath(),
				},
			},
		},
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-discovery",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Volumes:            volumes,
					ServiceAccountName: "local-storage-operator", // TODO replace with common var
					Containers: []corev1.Container{
						{
							Image: util.GetDiskMakerImage(),
							Args:  []string{"lv-manager"},
							Name:  "local-diskmaker",
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:             "local-disks",
									MountPath:        util.GetLocalDiskLocationPath(),
									MountPropagation: &hostContainerPropagation,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: "MY_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name: "WATCH_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	addOwnerLabels(&ds.ObjectMeta, cr)
	return ds
}

// syncProvisionerConfigMap syncs the configmap and returns any error if occured
func (r *ReconcileLocalVolumeSet) syncProvisionerConfigMap(cr *localv1alpha1.LocalVolumeSet) error {
	provisionerConfigMap, err := r.generateProvisionerConfigMap(cr)
	if err != nil {
		klog.Errorf("error generating provisioner configmap %s: %v", cr.Name, err)
		return err
	}
	oldProvisionerConfigMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: provisionerConfigMap.Name, Namespace: provisionerConfigMap.Namespace}}
	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, oldProvisionerConfigMap, func() error {
		if oldProvisionerConfigMap.Labels == nil {
			oldProvisionerConfigMap.Labels = make(map[string]string)
		}
		for k, v := range provisionerConfigMap.Labels {
			oldProvisionerConfigMap.Labels[k] = v
		}
		oldProvisionerConfigMap.Data = provisionerConfigMap.Data
		return controllerutil.SetControllerReference(cr, oldProvisionerConfigMap, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileLocalVolumeSet) getProvisionerConfigMapName(cr *localv1alpha1.LocalVolumeSet) string {
	return cr.Name + "-localvolumeset-local-provisioner-configmap"
}

// generateProvisionerConfigMap Create configmap requires by the local storage provisioner
func (r *ReconcileLocalVolumeSet) generateProvisionerConfigMap(cr *localv1alpha1.LocalVolumeSet) (*corev1.ConfigMap, error) {
	provisonerConfigName := r.getProvisionerConfigMapName(cr)
	configMapData := map[string]string{}
	configMapData["fstype"] = cr.Spec.FSType
	configMapData["volumeMode"] = string(cr.Spec.VolumeMode)
	configMapData["storageClassName"] = cr.Spec.StorageClassName
	pvLables, err := yaml.Marshal(pvLabels(cr))
	if err != nil {
		return nil, fmt.Errorf("error creating configmap while marshaling yaml: %+v", err)
	}
	configMapData["pvLables"] = string(pvLables)

	configmap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisonerConfigName,
			Labels:    provisionerLabels(cr.Name),
			Namespace: cr.Namespace,
		},
	}

	configmap.Data = configMapData
	addOwnerLabels(&configmap.ObjectMeta, cr)
	return configmap, nil
}

func provisionerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volumeset-provisioner-%s", crName),
	}
}

// name of the CR that owns this local volume
func pvLabels(lvs *localv1alpha1.LocalVolumeSet) map[string]string {
	return map[string]string{
		commontypes.LocalVolumeOwnerNameForPV:      lvs.Name,
		commontypes.LocalVolumeOwnerNamespaceForPV: lvs.Namespace,
	}
}

func addOwnerLabels(meta *metav1.ObjectMeta, cr *localv1alpha1.LocalVolumeSet) {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	if v, exists := meta.Labels[LocalVolumeSetNameLabel]; !exists || v != cr.Namespace {
		meta.Labels[LocalVolumeSetNameLabel] = cr.Namespace
	}
	if v, exists := meta.Labels[LocalVolumeSetNamespaceLabel]; !exists || v != cr.Name {
		meta.Labels[LocalVolumeSetNamespaceLabel] = cr.Name
	}
}

func (r *ReconcileLocalVolumeSet) syncProvisionerDaemonset(cr *localv1alpha1.LocalVolumeSet, provisionerConfigMapName string) error {
	ds := r.generateLocalProvisionerDaemonset(cr, provisionerConfigMapName)
	oldDs := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: ds.Name, Namespace: ds.Namespace}}
	_, err := controllerutil.CreateOrUpdate(context.TODO(), r.client, oldDs, func() error {
		if oldDs.Labels == nil {
			oldDs.Labels = make(map[string]string)
		}
		for k, v := range ds.Labels {
			oldDs.Labels[k] = v
		}
		oldDs.Spec = ds.Spec
		return controllerutil.SetControllerReference(cr, oldDs, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileLocalVolumeSet) syncRBACPolicies(cr *localv1alpha1.LocalVolumeSet) error {
	operatorLabel := map[string]string{
		"openshift-operator": "local-storage-operator",
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.ProvisionerServiceAccount,
			Namespace: cr.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(context.TODO(), r.client, serviceAccount, func() error {
		if serviceAccount.Labels == nil {
			serviceAccount.Labels = make(map[string]string)
		}
		for k, v := range operatorLabel {
			serviceAccount.Labels[k] = v
		}
		return controllerutil.SetOwnerReference(cr, serviceAccount, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating serviceAccount %s : %v", serviceAccount.Name, err)
	}

	provisionerClusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.ProvisionerNodeRoleName,
			Namespace: cr.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, provisionerClusterRole, func() error {
		if provisionerClusterRole.Labels == nil {
			provisionerClusterRole.Labels = make(map[string]string)
			for k, v := range operatorLabel {
				provisionerClusterRole.Labels[k] = v
			}
			provisionerClusterRole.Rules = []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"nodes"},
				},
			}
		}
		return controllerutil.SetOwnerReference(cr, provisionerClusterRole, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating provisionerClusterRole %s : %v", provisionerClusterRole.Name, err)
	}

	pvClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.ProvisionerPVRoleBindingName,
			Namespace: cr.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, pvClusterRoleBinding, func() error {
		if pvClusterRoleBinding.Labels == nil {
			pvClusterRoleBinding.Labels = make(map[string]string)
		}
		for k, v := range operatorLabel {
			pvClusterRoleBinding.Labels[k] = v
		}
		pvClusterRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		}
		pvClusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     util.DefaultPVClusterRole,
		}
		return controllerutil.SetOwnerReference(cr, pvClusterRoleBinding, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating pvClusterRoleBinding %s : %v", pvClusterRoleBinding.Name, err)
	}

	nodeRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.ProvisionerNodeRoleBindingName,
			Namespace: cr.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, nodeRoleBinding, func() error {
		if nodeRoleBinding.Labels == nil {
			nodeRoleBinding.Labels = make(map[string]string)
		}
		for k, v := range operatorLabel {
			nodeRoleBinding.Labels[k] = v
		}
		nodeRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		}
		nodeRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     provisionerClusterRole.Name,
		}
		return controllerutil.SetOwnerReference(cr, nodeRoleBinding, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating nodeRoleBinding %s : %v", nodeRoleBinding.Name, err)
	}

	localVolumeRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.LocalVolumeRoleName,
			Namespace: cr.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, localVolumeRole, func() error {
		if localVolumeRole.Labels == nil {
			localVolumeRole.Labels = make(map[string]string)
		}
		for k, v := range operatorLabel {
			localVolumeRole.Labels[k] = v
		}
		localVolumeRole.Rules = []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "list", "watch"},
				APIGroups: []string{"local.storage.openshift.io"},
				Resources: []string{"*"},
			},
		}
		return controllerutil.SetOwnerReference(cr, localVolumeRole, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating localVolumeRole %s : %v", localVolumeRole.Name, err)
	}

	localVolumeRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + util.LocalVolumeRoleBindingName,
			Namespace: cr.Namespace,
			Labels:    operatorLabel,
		},
	}
	_, err = controllerutil.CreateOrUpdate(context.TODO(), r.client, localVolumeRoleBinding, func() error {
		if localVolumeRoleBinding.Labels == nil {
			localVolumeRoleBinding.Labels = make(map[string]string)
		}
		for k, v := range operatorLabel {
			localVolumeRoleBinding.Labels[k] = v
		}
		localVolumeRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		}
		localVolumeRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     localVolumeRole.Name,
		}
		return controllerutil.SetOwnerReference(cr, localVolumeRoleBinding, r.scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating or updating localVolumeRoleBinding %s : %v", localVolumeRoleBinding.Name, err)
	}
	return nil
}

func (r *ReconcileLocalVolumeSet) generateLocalProvisionerDaemonset(cr *localv1alpha1.LocalVolumeSet, provisionerConfigMapName string) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	directoryHostPath := corev1.HostPathDirectory
	containers := []corev1.Container{
		{
			Name:  "local-storage-provisioner",
			Image: util.GetLocalProvisionerImage(),
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "provisioner-config",
					ReadOnly:  true,
					MountPath: "/etc/provisioner/config",
				},
				{
					Name:             "local-disks",
					MountPath:        util.GetLocalDiskLocationPath(),
					MountPropagation: &hostContainerPropagation,
				},
				{
					Name:             "device-dir",
					MountPath:        "/dev",
					MountPropagation: &hostContainerPropagation,
				},
			},
		},
	}
	volumes := []corev1.Volume{
		{
			Name: "provisioner-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: provisionerConfigMapName,
					},
				},
			},
		},
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: util.GetLocalDiskLocationPath(),
				},
			},
		},
		{
			Name: "device-dir",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/dev",
					Type: &directoryHostPath,
				},
			},
		},
	}
	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-local-provisioner",
			Namespace: cr.Namespace,
			Labels:    provisionerLabels(cr.Name),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: provisionerLabels(cr.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: provisionerLabels(cr.Name),
				},
				Spec: corev1.PodSpec{
					Containers:         containers,
					ServiceAccountName: cr.Name + "-" + util.ProvisionerServiceAccount,
					Tolerations:        cr.Spec.Tolerations,
					Volumes:            volumes,
				},
			},
		},
	}
	r.applyNodeSelector(cr, ds)
	addOwnerLabels(&ds.ObjectMeta, cr)
	return ds
}
