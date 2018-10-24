package controller

import (
	"context"
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Handler returns a Handler for running the operator
type Handler struct {
	// Fill me
	localStorageNameSpace string
	localDiskLocation     string
	provisonerConfigName  string
	diskMakerConfigName   string
}

type localDiskData map[string]map[string]string

const (
	localDiskLocation              = "/mnt/local-storage"
	provisionerServiceAccount      = "local-storage-admin"
	provisionerPVRoleBindingName   = "local-storage-provisioner-pv-binding"
	provisionerNodeRoleName        = "local-storage-provisioner-node-clusterrole"
	defaultPVClusterRole           = "system:persistent-volume-provisioner"
	provisionerNodeRoleBindingName = "local-storage-provisioner-node-binding"
	ownerNamespaceLabel            = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel                 = "local.storage.openshift.io/owner-name"
)

// NewHandler returns a controller handler
func NewHandler(namespace string) sdk.Handler {
	handler := &Handler{
		localStorageNameSpace: namespace,
		localDiskLocation:     localDiskLocation,
	}
	return handler
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.LocalStorageProvider:
		if event.Deleted {
			// TODO: Handle deletion later
			return nil
		}
		var err error
		err = h.syncRbacPolicies(o)
		if err != nil {
			logrus.Error(err)
			return err
		}

		provisionerConfigMapModified, err := h.syncProvisionerConfigMap(o)
		if err != nil {
			logrus.Errorf("error creating provisioner configmap %s with %v", o.Name, err)
			return err
		}

		diskMakerConfigMapModified, err := h.syncDiskMakerConfigMap(o)
		if err != nil {
			logrus.Errorf("error creating diskmaker configmap %s with %v", o.Name, err)
			return err
		}

		err = h.syncStorageClass(o)
		if err != nil {
			logrus.Errorf("failed to create storageClass %v", err)
			return err
		}

		err = sdk.Create(h.createLocalProvisionerDaemonset(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create daemonset for provisioner %s with %v", o.Name, err)
			return err
		}

		err = sdk.Create(h.createDiskMakerDaemonSet(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create daemonset for diskmaker %s with %v", o.Name, err)
			return err
		}

	}
	return nil
}

// syncProvisionerConfigMap syncs the configmap and returns true if configmap was modified
func (h *Handler) syncProvisionerConfigMap(o *v1alpha1.LocalStorageProvider) (bool, error) {
	provisionerConfigMap, err := h.generateProvisionerConfigMap(o)
	if err != nil {
		logrus.Errorf("error generating provisioner configmap %s with %v", o.Name, err)
		return false, err
	}
	provisionerConfigMap, modified, err := resourceapply.ApplyConfigMap(k8sclient.GetKubeClient().CoreV1(), provisionerConfigMap)
	if err != nil {
		return false, fmt.Errorf("error creating provisioner configmap %s with %v", o.Name, err)
	}
	return modified, nil
}

func (h *Handler) syncDiskMakerConfigMap(o *v1alpha1.LocalStorageProvider) (bool, error) {
	diskMakerConfigMap, err := h.generateDiskMakerConfig(o)
	if err != nil {
		return false, fmt.Errorf("error generating diskmaker configmap %s with %v", o.Name, err)
	}
	diskMakerConfigMap, modified, err := resourceapply.ApplyConfigMap(k8sclient.GetKubeClient().CoreV1(), diskMakerConfigMap)
	if err != nil {
		return false, fmt.Errorf("error creating diskmarker configmap %s with %v", o.Name, err)
	}
	return modified, nil
}

func (h *Handler) syncRbacPolicies(o *v1alpha1.LocalStorageProvider) error {
	operatorLabel := map[string]string{
		"openshift-operator": "local-storage-operator",
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerServiceAccount,
			Namespace: o.Namespace,
			Labels:    operatorLabel,
		},
	}
	serviceAccount, _, err := resourceapply.ApplyServiceAccount(k8sclient.GetKubeClient().CoreV1(), serviceAccount)
	if err != nil {
		return fmt.Errorf("error applying service account %s with %v", serviceAccount.Name, err)
	}

	provisionerClusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerNodeRoleName,
			Namespace: o.Namespace,
			Labels:    operatorLabel,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"nodes"},
			},
		},
	}
	provisionerClusterRole, _, err = resourceapply.ApplyClusterRole(k8sclient.GetKubeClient().RbacV1(), provisionerClusterRole)
	if err != nil {
		return fmt.Errorf("error applying cluster role %s with %v", provisionerClusterRole.Name, err)
	}

	pvClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerPVRoleBindingName,
			Namespace: o.Namespace,
			Labels:    operatorLabel,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     defaultPVClusterRole,
		},
	}

	pvClusterRoleBinding, _, err = resourceapply.ApplyClusterRoleBinding(k8sclient.GetKubeClient().RbacV1(), pvClusterRoleBinding)
	if err != nil {
		return fmt.Errorf("error applying pv cluster role binding %s with %v", pvClusterRoleBinding.Name, err)
	}

	nodeRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerNodeRoleBindingName,
			Namespace: o.Namespace,
			Labels:    operatorLabel,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     provisionerClusterRole.Name,
		},
	}
	nodeRoleBinding, _, err = resourceapply.ApplyClusterRoleBinding(k8sclient.GetKubeClient().RbacV1(), nodeRoleBinding)
	if err != nil {
		return fmt.Errorf("error creating node role binding %s with %v", nodeRoleBinding.Name, err)
	}
	return nil
}

// CreateConfigMap Create configmap requires by the local storage provisioner
func (h *Handler) generateProvisionerConfigMap(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	h.provisonerConfigName = cr.Name + "-local-provisioner-configmap"
	configMapData := make(localDiskData)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		storageClassData := map[string]string{}
		storageClassData["fstype"] = storageClassDevice.FSType
		storageClassData["volumeMode"] = string(storageClassDevice.VolumeMode)
		storageClassData["hostDir"] = fmt.Sprintf("%s/%s", h.localDiskLocation, storageClassName)
		storageClassData["mountDir"] = fmt.Sprintf("%s/%s", h.localDiskLocation, storageClassName)
		configMapData[storageClassName] = storageClassData
	}
	configmap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.provisonerConfigName,
			Labels:    provisionerLabels(cr.Name),
			Namespace: cr.Namespace,
		},
	}
	y, err := yaml.Marshal(configMapData)
	if err != nil {
		return nil, fmt.Errorf("error creating configmap while marshalling yaml %v", err)
	}
	configmap.Data = map[string]string{
		"storageClassMap": string(y),
	}
	addOwnerLabels(&configmap.ObjectMeta, cr)
	addOwner(&configmap.ObjectMeta, cr)
	return configmap, nil
}

func (h *Handler) syncStorageClass(cr *v1alpha1.LocalStorageProvider) error {
	storageClassDevices := cr.Spec.StorageClassDevices
	expectedStorageClasses := sets.NewString()
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		expectedStorageClasses.Insert(storageClassName)
		storageClass := generateStorageClass(cr, storageClassName)
		storageClass, _, err := applyStorageClass(k8sclient.GetKubeClient().StorageV1(), storageClass)
		if err != nil {
			return fmt.Errorf("error creating storageClass %s with %v", storageClassName, err)
		}
	}
	removeErrors := h.removeUnExpectedStorageClasses(cr, expectedStorageClasses)
	// For now we will ignore errors while removing unexpected storageClasses
	if removeErrors != nil {
		logrus.Errorf("error removing unexpected storageclasses : %v", removeErrors)
	}
	return nil
}

func (h *Handler) removeUnExpectedStorageClasses(cr *v1alpha1.LocalStorageProvider, expectedStorageClasses sets.String) error {
	list, err := k8sclient.
		GetKubeClient().StorageV1().
		StorageClasses().List(metav1.ListOptions{LabelSelector: getOwnerLabelSelector(cr).String()})
	if err != nil {
		return fmt.Errorf("error listing storageclasses for CR %s with %v", cr.Name, err)
	}
	removeErrors := []error{}
	for _, sc := range list.Items {
		if !expectedStorageClasses.Has(sc.Name) {
			logrus.Infof("removing storageClass %s", sc.Name)
			scDeleteErr := k8sclient.GetKubeClient().StorageV1().StorageClasses().Delete(sc.Name, nil)
			if scDeleteErr != nil && !errors.IsNotFound(scDeleteErr) {
				removeErrors = append(removeErrors, fmt.Errorf("error deleting storageclass %s with %v", sc.Name, scDeleteErr))
			}
		}
	}
	return utilerrors.NewAggregate(removeErrors)
}

func (h *Handler) generateDiskMakerConfig(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	h.diskMakerConfigName = cr.Name + "-diskmaker-configmap"
	configMapData := make(diskmaker.DiskConfig)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		disks := new(diskmaker.Disks)
		if len(storageClassDevice.DeviceNames) > 0 {
			disks.DiskNames = storageClassDevice.DeviceNames
		} else if len(storageClassDevice.DeviceIDs) > 0 {
			disks.DeviceIDs = storageClassDevice.DeviceIDs
		}
		configMapData[storageClassDevice.StorageClassName] = disks
	}

	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.diskMakerConfigName,
			Labels:    diskMakerLabels(cr.Name),
			Namespace: cr.Namespace,
		},
	}
	yaml, err := configMapData.ToYAML()
	if err != nil {
		return nil, err
	}
	configMap.Data = map[string]string{
		"diskMakerConfig": yaml,
	}
	addOwnerLabels(&configMap.ObjectMeta, cr)
	addOwner(&configMap.ObjectMeta, cr)
	return configMap, nil
}

func (h *Handler) syncDiskMakerDaemonset(cr *v1alpha1.LocalStorageProvider, forceRollout bool) error {
	return nil
}

func (h *Handler) syncProvisionerDaemonset(cr *v1alpha1.LocalStorageProvider, forceRollout bool) error {
	return nil
}

func (h *Handler) createLocalProvisionerDaemonset(cr *v1alpha1.LocalStorageProvider) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	containers := []corev1.Container{
		{
			Name:  "local-storage-provisioner",
			Image: cr.Spec.ProvisionerImage,
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
					MountPath:        h.localDiskLocation,
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
						Name: h.provisonerConfigName,
					},
				},
			},
		},
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: h.localDiskLocation,
				},
			},
		},
	}
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "local-provisioner",
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
					ServiceAccountName: "local-storage-admin",
					Volumes:            volumes,
				},
			},
		},
	}
}

func (h *Handler) createDiskMakerDaemonSet(cr *v1alpha1.LocalStorageProvider) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	containers := []corev1.Container{
		{
			Name:  "local-diskmaker",
			Image: cr.Spec.DiskMakerImage,
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
					MountPath: "/etc/local-storage-operator/config",
				},
				{
					Name:             "local-disks",
					MountPath:        h.localDiskLocation,
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
						Name: h.diskMakerConfigName,
					},
				},
			},
		},
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: h.localDiskLocation,
				},
			},
		},
	}
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "local-diskmaker",
			Namespace: cr.Namespace,
			Labels:    diskMakerLabels(cr.Name),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: diskMakerLabels(cr.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: diskMakerLabels(cr.Name),
				},
				Spec: corev1.PodSpec{
					Containers:         containers,
					ServiceAccountName: "local-storage-admin",
					Volumes:            volumes,
				},
			},
		},
	}
}

func addOwner(meta *metav1.ObjectMeta, cr *v1alpha1.LocalStorageProvider) {
	trueVal := true
	meta.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       v1alpha1.LocalStorageProviderKind,
			Name:       cr.Name,
			UID:        cr.UID,
			Controller: &trueVal,
		},
	}
}

func addOwnerLabels(meta *metav1.ObjectMeta, cr *v1alpha1.LocalStorageProvider) bool {
	changed := false
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
		changed = true
	}
	if v, exists := meta.Labels[ownerNamespaceLabel]; !exists || v != cr.Namespace {
		meta.Labels[ownerNamespaceLabel] = cr.Namespace
		changed = true
	}
	if v, exists := meta.Labels[ownerNameLabel]; !exists || v != cr.Name {
		meta.Labels[ownerNameLabel] = cr.Name
		changed = true
	}

	return changed
}

func diskMakerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volume-diskmaker-%s", crName),
	}
}

func provisionerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volume-provisioner-%s", crName),
	}
}

func generateStorageClass(cr *v1alpha1.LocalStorageProvider, scName string) *storagev1.StorageClass {
	deleteReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	firstConsumerBinding := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: scName,
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		ReclaimPolicy:     &deleteReclaimPolicy,
		VolumeBindingMode: &firstConsumerBinding,
	}
	addOwnerLabels(&sc.ObjectMeta, cr)
	addOwner(&sc.ObjectMeta, cr)
	return sc
}

func getOwnerLabelSelector(cr *v1alpha1.LocalStorageProvider) labels.Selector {
	ownerLabels := labels.Set{
		ownerNamespaceLabel: cr.Namespace,
		ownerNameLabel:      cr.Name,
	}
	return labels.SelectorFromSet(ownerLabels)
}
