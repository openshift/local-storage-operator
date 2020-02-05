package controller

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/ghodss/yaml"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

// Handler returns a Handler for running the operator
type Handler struct {
	// Fill me
	localStorageNameSpace string
	localDiskLocation     string
	provisonerConfigName  string
	diskMakerConfigName   string
	lock                  sync.Mutex
	apiClient             apiUpdater
}

type localDiskData map[string]map[string]string

const (
	// Name of the component
	componentName = "local-storage-operator"

	localDiskLocation            = "/mnt/local-storage"
	provisionerServiceAccount    = "local-storage-admin"
	provisionerPVRoleBindingName = "local-storage-provisioner-pv-binding"
	provisionerNodeRoleName      = "local-storage-provisioner-node-clusterrole"

	localVolumeRoleName        = "local-storage-provisioner-cr-role"
	localVolumeRoleBindingName = "local-storage-provisioner-cr-rolebinding"

	defaultPVClusterRole           = "system:persistent-volume-provisioner"
	provisionerNodeRoleBindingName = "local-storage-provisioner-node-binding"
	ownerNamespaceLabel            = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel                 = "local.storage.openshift.io/owner-name"

	defaultDiskMakerImageVersion = "quay.io/openshift/origin-local-storage-diskmaker"
	defaultProvisionImage        = "quay.io/openshift/origin-local-storage-static-provisioner"

	diskMakerImageEnv   = "DISKMAKER_IMAGE"
	provisionerImageEnv = "PROVISIONER_IMAGE"

	localVolumeFinalizer = "storage.openshift.com/local-volume-protection"
)

// NewHandler returns a controller handler
func NewHandler(namespace string) sdk.Handler {
	handler := &Handler{
		localStorageNameSpace: namespace,
		localDiskLocation:     localDiskLocation,
	}
	apiClient, err := newAPIUpdater(namespace)
	if err != nil {
		klog.Fatalf("error creating API client for api-server: %v", err)
	}
	handler.apiClient = apiClient
	return handler
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	var localStorageProvider *localv1.LocalVolume
	switch o := event.Object.(type) {
	case *localv1.LocalVolume:
		if event.Deleted {
			return nil
		}
		localStorageProvider = o
	default:
		klog.V(2).Infof("Unexpected kind of object: %+v", o)
		return fmt.Errorf("expected object: %+v", o)
	}

	if localStorageProvider != nil {
		return h.syncLocalVolumeProvider(localStorageProvider)
	}
	return nil
}

func (h *Handler) syncLocalVolumeProvider(instance *localv1.LocalVolume) error {
	h.lock.Lock()
	defer h.lock.Unlock()

	var err error
	// Create a copy so as we don't modify original LocalVolume
	o := instance.DeepCopy()
	// set default image version etc
	o.SetDefaults()

	if isDeletionCandidate(o, localVolumeFinalizer) {
		return h.cleanupLocalVolumeDeployment(o)
	}

	// Lets add a finalizer to the LocalVolume object first
	o, modified := addFinalizer(o)
	if modified {
		return h.apiClient.updateLocalVolume(o)
	}

	if o.Spec.ManagementState != operatorv1.Managed && o.Spec.ManagementState != operatorv1.Force {
		klog.Infof("operator is not managing local volumes: %v", o.Spec.ManagementState)
		o.Status.State = o.Spec.ManagementState
		err = h.apiClient.syncStatus(instance, o)
		if err != nil {
			return fmt.Errorf("error syncing status: %v", err)
		}
		return nil
	}

	err = h.syncRBACPolicies(o)
	if err != nil {
		klog.Error(err)
		return h.addFailureCondition(instance, o, err)
	}

	provisionerConfigMapModified, err := h.syncProvisionerConfigMap(o)
	if err != nil {
		klog.Errorf("error creating provisioner configmap %s: %v", o.Name, err)
		return h.addFailureCondition(instance, o, err)
	}

	diskMakerConfigMapModified, err := h.syncDiskMakerConfigMap(o)
	if err != nil {
		klog.Errorf("error creating diskmaker configmap %s: %v", o.Name, err)
		return h.addFailureCondition(instance, o, err)
	}

	err = h.syncStorageClass(o)
	if err != nil {
		klog.Errorf("failed to create storageClass: %v", err)
		return h.addFailureCondition(instance, o, err)
	}

	rollOutDaemonSet := false
	if diskMakerConfigMapModified || provisionerConfigMapModified {
		rollOutDaemonSet = true
	}

	if o.Status.ObservedGeneration != nil &&
		(*o.Status.ObservedGeneration != o.Generation) {
		rollOutDaemonSet = true
	}

	children := []operatorv1.GenerationStatus{}

	provisionerDS, err := h.syncProvisionerDaemonset(o, rollOutDaemonSet)
	if err != nil {
		klog.Errorf("failed to create daemonset for provisioner %s: %v", o.Name, err)
		return h.addFailureCondition(instance, o, err)
	}

	if provisionerDS != nil {
		children = append(children, operatorv1.GenerationStatus{
			Group:          appsv1.GroupName,
			Resource:       "DaemonSet",
			Namespace:      provisionerDS.Namespace,
			Name:           provisionerDS.Name,
			LastGeneration: provisionerDS.Generation,
		})
	}

	diskMakerDaemonset, err := h.syncDiskMakerDaemonset(o, rollOutDaemonSet)
	if err != nil {
		klog.Errorf("failed to create daemonset for diskmaker %s: %v", o.Name, err)
		return h.addFailureCondition(instance, o, err)
	}
	if diskMakerDaemonset != nil {
		children = append(children, operatorv1.GenerationStatus{
			Group:          appsv1.GroupName,
			Resource:       "DaemonSet",
			Namespace:      diskMakerDaemonset.Namespace,
			Name:           diskMakerDaemonset.Name,
			LastGeneration: diskMakerDaemonset.Generation,
		})
	}
	o.Status.Generations = children
	o.Status.State = operatorv1.Managed
	o = h.addSuccessCondition(o)
	o.Status.ObservedGeneration = &o.Generation
	err = h.apiClient.syncStatus(instance, o)
	if err != nil {
		klog.Errorf("error syncing status: %v", err)
		return fmt.Errorf("error syncing status: %v", err)
	}
	return nil
}

func (h *Handler) addFailureCondition(oldLv *localv1.LocalVolume, lv *localv1.LocalVolume, err error) error {
	message := fmt.Sprintf("error syncing local storage: %+v", err)
	condition := operatorv1.OperatorCondition{
		Type:               operatorv1.OperatorStatusTypeAvailable,
		Status:             operatorv1.ConditionFalse,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	newConditions := []operatorv1.OperatorCondition{condition}
	lv.Status.Conditions = newConditions
	syncErr := h.apiClient.syncStatus(oldLv, lv)
	if syncErr != nil {
		klog.Errorf("error syncing condition: %v", syncErr)
	}
	return err
}

func (h *Handler) addSuccessCondition(lv *localv1.LocalVolume) *localv1.LocalVolume {
	condition := operatorv1.OperatorCondition{
		Type:               operatorv1.OperatorStatusTypeAvailable,
		Status:             operatorv1.ConditionTrue,
		Message:            "Ready",
		LastTransitionTime: metav1.Now(),
	}
	newConditions := []operatorv1.OperatorCondition{condition}
	oldConditions := lv.Status.Conditions
	for _, c := range oldConditions {
		// if operator already has success condition - don't add again
		if c.Type == operatorv1.OperatorStatusTypeAvailable &&
			c.Status == operatorv1.ConditionTrue &&
			c.Message == "Ready" {
			return lv
		}
	}
	lv.Status.Conditions = newConditions
	return lv
}

func (h *Handler) localProvisionerImage() string {
	if provisionerImageFromEnv := os.Getenv(provisionerImageEnv); provisionerImageFromEnv != "" {
		return provisionerImageFromEnv
	}
	return defaultProvisionImage
}

func (h *Handler) diskMakerImage() string {
	if diskMakerImageFromEnv := os.Getenv(diskMakerImageEnv); diskMakerImageFromEnv != "" {
		return diskMakerImageFromEnv
	}
	return defaultDiskMakerImageVersion
}

func (h *Handler) cleanupLocalVolumeDeployment(lv *localv1.LocalVolume) error {
	klog.Infof("Deleting localvolume: %s", commontypes.LocalVolumeKey(lv))
	childPersistentVolumes, err := h.apiClient.listPersistentVolumes(metav1.ListOptions{LabelSelector: commontypes.GetPVOwnerSelector(lv).String()})
	if err != nil {
		msg := fmt.Sprintf("error listing persistent volumes for localvolume %s: %v", commontypes.LocalVolumeKey(lv), err)
		h.apiClient.recordEvent(lv, corev1.EventTypeWarning, listingPersistentVolumesFailed, msg)
		return fmt.Errorf(msg)
	}
	boundPVs := []corev1.PersistentVolume{}
	for _, pv := range childPersistentVolumes.Items {
		if pv.Status.Phase == corev1.VolumeBound {
			boundPVs = append(boundPVs, pv)
		}
	}
	if len(boundPVs) > 0 {
		msg := fmt.Sprintf("localvolume %s has bound persistentvolumes in use", commontypes.LocalVolumeKey(lv))
		h.apiClient.recordEvent(lv, corev1.EventTypeWarning, localVolumeDeletionFailed, msg)
		return fmt.Errorf(msg)
	}

	lv = removeFinalizer(lv)
	return h.apiClient.updateLocalVolume(lv)
}

// syncProvisionerConfigMap syncs the configmap and returns true if configmap was modified
func (h *Handler) syncProvisionerConfigMap(o *localv1.LocalVolume) (bool, error) {
	provisionerConfigMap, err := h.generateProvisionerConfigMap(o)
	if err != nil {
		klog.Errorf("error generating provisioner configmap %s: %v", o.Name, err)
		return false, err
	}
	_, modified, err := h.apiClient.applyConfigMap(provisionerConfigMap)
	if err != nil {
		return false, fmt.Errorf("error creating provisioner configmap %s: %v", o.Name, err)
	}
	return modified, nil
}

func (h *Handler) syncDiskMakerConfigMap(o *localv1.LocalVolume) (bool, error) {
	diskMakerConfigMap, err := h.generateDiskMakerConfig(o)
	if err != nil {
		return false, fmt.Errorf("error generating diskmaker configmap %s: %v", o.Name, err)
	}
	_, modified, err := h.apiClient.applyConfigMap(diskMakerConfigMap)
	if err != nil {
		return false, fmt.Errorf("error creating diskmarker configmap %s: %v", o.Name, err)
	}
	return modified, nil
}

func (h *Handler) syncRBACPolicies(o *localv1.LocalVolume) error {
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
	addOwner(&serviceAccount.ObjectMeta, o)

	_, _, err := h.apiClient.applyServiceAccount(serviceAccount)
	if err != nil {
		return fmt.Errorf("error applying service account %s: %v", serviceAccount.Name, err)
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
	addOwner(&provisionerClusterRole.ObjectMeta, o)
	_, _, err = h.apiClient.applyClusterRole(provisionerClusterRole)
	if err != nil {
		return fmt.Errorf("error applying cluster role %s: %v", provisionerClusterRole.Name, err)
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
	addOwner(&pvClusterRoleBinding.ObjectMeta, o)

	_, _, err = h.apiClient.applyClusterRoleBinding(pvClusterRoleBinding)
	if err != nil {
		return fmt.Errorf("error applying pv cluster role binding %s: %v", pvClusterRoleBinding.Name, err)
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
	addOwner(&nodeRoleBinding.ObjectMeta, o)

	_, _, err = h.apiClient.applyClusterRoleBinding(nodeRoleBinding)
	if err != nil {
		return fmt.Errorf("error creating node role binding %s: %v", nodeRoleBinding.Name, err)
	}

	localVolumeRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      localVolumeRoleName,
			Namespace: o.Namespace,
			Labels:    operatorLabel,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{"local.storage.openshift.io"},
				Resources: []string{"*"},
			},
		},
	}
	addOwner(&localVolumeRole.ObjectMeta, o)
	_, _, err = h.apiClient.applyRole(localVolumeRole)
	if err != nil {
		return fmt.Errorf("error applying localvolume role %s: %v", localVolumeRole.Name, err)
	}

	localVolumeRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      localVolumeRoleBindingName,
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
			Kind:     "Role",
			Name:     localVolumeRole.Name,
		},
	}

	addOwner(&localVolumeRoleBinding.ObjectMeta, o)
	_, _, err = h.apiClient.applyRoleBinding(localVolumeRoleBinding)
	if err != nil {
		return fmt.Errorf("error applying localvolume rolebinding %s: %v", localVolumeRoleBinding.Name, err)
	}
	return nil
}

// CreateConfigMap Create configmap requires by the local storage provisioner
func (h *Handler) generateProvisionerConfigMap(cr *localv1.LocalVolume) (*corev1.ConfigMap, error) {
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
		return nil, fmt.Errorf("error creating configmap while marshalling yaml: %v", err)
	}

	pvLabelString, err := yaml.Marshal(pvLabels(cr))
	if err != nil {
		return nil, fmt.Errorf("error generating pv labels: %v", err)
	}

	configmap.Data = map[string]string{
		"storageClassMap": string(y),
		"labelsForPV":     string(pvLabelString),
	}
	addOwnerLabels(&configmap.ObjectMeta, cr)
	addOwner(&configmap.ObjectMeta, cr)
	return configmap, nil
}

func (h *Handler) syncStorageClass(cr *localv1.LocalVolume) error {
	storageClassDevices := cr.Spec.StorageClassDevices
	expectedStorageClasses := sets.NewString()
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		expectedStorageClasses.Insert(storageClassName)
		storageClass := generateStorageClass(cr, storageClassName)
		_, _, err := h.apiClient.applyStorageClass(storageClass)
		if err != nil {
			return fmt.Errorf("error creating storageClass %s: %v", storageClassName, err)
		}
	}
	removeErrors := h.removeUnExpectedStorageClasses(cr, expectedStorageClasses)
	// For now we will ignore errors while removing unexpected storageClasses
	if removeErrors != nil {
		klog.Errorf("error removing unexpected storageclasses: %v", removeErrors)
	}
	return nil
}

func (h *Handler) removeUnExpectedStorageClasses(cr *localv1.LocalVolume, expectedStorageClasses sets.String) error {
	list, err := h.apiClient.listStorageClasses(metav1.ListOptions{LabelSelector: getOwnerLabelSelector(cr).String()})
	if err != nil {
		return fmt.Errorf("error listing storageclasses for CR %s: %v", cr.Name, err)
	}
	removeErrors := []error{}
	for _, sc := range list.Items {
		if !expectedStorageClasses.Has(sc.Name) {
			klog.Infof("removing storageClass %s", sc.Name)
			scDeleteErr := k8sclient.GetKubeClient().StorageV1().StorageClasses().Delete(sc.Name, nil)
			if scDeleteErr != nil && !errors.IsNotFound(scDeleteErr) {
				removeErrors = append(removeErrors, fmt.Errorf("error deleting storageclass %s: %v", sc.Name, scDeleteErr))
			}
		}
	}
	return utilerrors.NewAggregate(removeErrors)
}

func (h *Handler) generateDiskMakerConfig(cr *localv1.LocalVolume) (*corev1.ConfigMap, error) {
	h.diskMakerConfigName = cr.Name + "-diskmaker-configmap"
	configMapData := &diskmaker.DiskConfig{
		Disks:           map[string]*diskmaker.Disks{},
		OwnerName:       cr.Name,
		OwnerNamespace:  cr.Namespace,
		OwnerKind:       localv1.LocalVolumeKind,
		OwnerUID:        string(cr.UID),
		OwnerAPIVersion: localv1.SchemeGroupVersion.String(),
	}

	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		disks := new(diskmaker.Disks)
		if len(storageClassDevice.DevicePaths) > 0 {
			disks.DevicePaths = storageClassDevice.DevicePaths
		}
		configMapData.Disks[storageClassDevice.StorageClassName] = disks
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

func (h *Handler) syncDiskMakerDaemonset(cr *localv1.LocalVolume, forceRollout bool) (*appsv1.DaemonSet, error) {
	ds := h.generateDiskMakerDaemonSet(cr)
	dsName := ds.Name
	generation := getExpectedGeneration(cr, ds)
	ds, _, err := h.apiClient.applyDaemonSet(ds, generation, forceRollout)
	if err != nil {
		return nil, fmt.Errorf("error applying diskmaker daemonset %s: %v", dsName, err)
	}
	return ds, nil
}

func (h *Handler) syncProvisionerDaemonset(cr *localv1.LocalVolume, forceRollout bool) (*appsv1.DaemonSet, error) {
	ds := h.generateLocalProvisionerDaemonset(cr)
	dsName := ds.Name
	generation := getExpectedGeneration(cr, ds)
	ds, _, err := h.apiClient.applyDaemonSet(ds, generation, forceRollout)
	if err != nil {
		return nil, fmt.Errorf("error applying provisioner daemonset %s: %v", dsName, err)
	}
	return ds, nil
}

func (h *Handler) generateLocalProvisionerDaemonset(cr *localv1.LocalVolume) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	directoryHostPath := corev1.HostPathDirectory
	containers := []corev1.Container{
		{
			Name:  "local-storage-provisioner",
			Image: h.localProvisionerImage(),
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
					ServiceAccountName: provisionerServiceAccount,
					Tolerations:        cr.Spec.Tolerations,
					Volumes:            volumes,
				},
			},
		},
	}
	h.applyNodeSelector(cr, ds)
	addOwner(&ds.ObjectMeta, cr)
	addOwnerLabels(&ds.ObjectMeta, cr)
	return ds
}

func (h *Handler) applyNodeSelector(cr *localv1.LocalVolume, ds *appsv1.DaemonSet) *appsv1.DaemonSet {
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

func (h *Handler) generateDiskMakerDaemonSet(cr *localv1.LocalVolume) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	containers := []corev1.Container{
		{
			Name:  "local-diskmaker",
			Image: h.diskMakerImage(),
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
				{
					Name:             "device-dir",
					MountPath:        "/dev",
					MountPropagation: &hostContainerPropagation,
				},
			},
		},
	}
	directoryHostPath := corev1.HostPathDirectory
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
			Name:      cr.Name + "-local-diskmaker",
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
					ServiceAccountName: provisionerServiceAccount,
					Tolerations:        cr.Spec.Tolerations,
					Volumes:            volumes,
				},
			},
		},
	}
	h.applyNodeSelector(cr, ds)
	addOwner(&ds.ObjectMeta, cr)
	addOwnerLabels(&ds.ObjectMeta, cr)
	return ds
}

func addFinalizer(lv *localv1.LocalVolume) (*localv1.LocalVolume, bool) {
	currentFinalizers := lv.GetFinalizers()
	if contains(currentFinalizers, localVolumeFinalizer) {
		return lv, false
	}
	lv.SetFinalizers(append(currentFinalizers, localVolumeFinalizer))
	return lv, true
}

func removeFinalizer(lv *localv1.LocalVolume) *localv1.LocalVolume {
	currentFinalizers := lv.GetFinalizers()
	if !contains(currentFinalizers, localVolumeFinalizer) {
		return lv
	}
	newFinalizers := remove(currentFinalizers, localVolumeFinalizer)
	lv.SetFinalizers(newFinalizers)
	return lv
}

func addOwner(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) {
	trueVal := true
	meta.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: localv1.SchemeGroupVersion.String(),
			Kind:       localv1.LocalVolumeKind,
			Name:       cr.Name,
			UID:        cr.UID,
			Controller: &trueVal,
		},
	}
}

func addOwnerLabels(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) bool {
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

// name of the CR that owns this local volume
func pvLabels(lv *localv1.LocalVolume) map[string]string {
	return map[string]string{
		commontypes.LocalVolumeOwnerNameForPV:      lv.Name,
		commontypes.LocalVolumeOwnerNamespaceForPV: lv.Namespace,
	}
}

func generateStorageClass(cr *localv1.LocalVolume, scName string) *storagev1.StorageClass {
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
	return sc
}

func getOwnerLabelSelector(cr *localv1.LocalVolume) labels.Selector {
	ownerLabels := labels.Set{
		ownerNamespaceLabel: cr.Namespace,
		ownerNameLabel:      cr.Name,
	}
	return labels.SelectorFromSet(ownerLabels)
}

func getExpectedGeneration(cr *localv1.LocalVolume, obj runtime.Object) int64 {
	gvk := obj.GetObjectKind().GroupVersionKind()
	var lastGeneration int64 = -1
	for _, child := range cr.Status.Generations {
		if child.Group != gvk.Group || child.Resource != gvk.Kind {
			continue
		}
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return -1
		}
		if child.Name != accessor.GetName() || child.Namespace != accessor.GetNamespace() {
			continue
		}
		lastGeneration = child.LastGeneration
	}
	return lastGeneration
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// isDeletionCandidate checks if object is candidate to be deleted
func isDeletionCandidate(obj metav1.Object, finalizer string) bool {
	return obj.GetDeletionTimestamp() != nil && contains(obj.GetFinalizers(), finalizer)
}
