package controller

import (
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
)

type apiUpdater interface {
	syncStatus(oldInstance, newInstance *localv1.LocalVolume) error
	applyServiceAccount(serviceAccount *corev1.ServiceAccount) (*corev1.ServiceAccount, bool, error)
	applyConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, bool, error)
	applyClusterRole(clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error)
	applyClusterRoleBinding(roleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error)
	applyRole(role *rbacv1.Role) (*rbacv1.Role, bool, error)
	applyRoleBinding(roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error)
	applyStorageClass(required *storagev1.StorageClass) (*storagev1.StorageClass, bool, error)
	applyDaemonSet(ds *appsv1.DaemonSet, expectedGeneration int64, forceRollout bool) (*appsv1.DaemonSet, bool, error)
	listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error)
	recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{})
}

type sdkAPIUpdater struct {
	recorder record.EventRecorder
}

func newAPIUpdater() apiUpdater {
	apiClient := &sdkAPIUpdater{}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(k8sclient.GetKubeClient().CoreV1().RESTClient()).Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: componentName})
	apiClient.recorder = recorder
	return apiClient
}

var _ apiUpdater = &sdkAPIUpdater{}

func (s *sdkAPIUpdater) syncStatus(oldInstance, newInstance *localv1.LocalVolume) error {
	klog.Info("Syncing LocalVolume.Status")

	if !equality.Semantic.DeepEqual(oldInstance.Status, newInstance.Status) {
		klog.Info("Updating LocalVolume.Status")
		err := sdk.Update(newInstance)
		if err != nil && errors.IsConflict(err) {
			s.recordEvent(newInstance, corev1.EventTypeWarning, localVolumeUpdateFailed, "updating localvolume failed with %v", err)
		}
		return err
	}
	return nil
}

func (s *sdkAPIUpdater) applyServiceAccount(sa *corev1.ServiceAccount) (*corev1.ServiceAccount, bool, error) {
	return resourceapply.ApplyServiceAccount(k8sclient.GetKubeClient().CoreV1(), sa)
}

func (s *sdkAPIUpdater) applyConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, bool, error) {
	return resourceapply.ApplyConfigMap(k8sclient.GetKubeClient().CoreV1(), configmap)
}

func (s *sdkAPIUpdater) applyRole(role *rbacv1.Role) (*rbacv1.Role, bool, error) {
	return resourceapply.ApplyRole(k8sclient.GetKubeClient().RbacV1(), role)
}

func (s *sdkAPIUpdater) applyRoleBinding(roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error) {
	return resourceapply.ApplyRoleBinding(k8sclient.GetKubeClient().RbacV1(), roleBinding)
}

func (s *sdkAPIUpdater) applyClusterRole(clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error) {
	return resourceapply.ApplyClusterRole(k8sclient.GetKubeClient().RbacV1(), clusterRole)
}

func (s *sdkAPIUpdater) applyClusterRoleBinding(roleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error) {
	return resourceapply.ApplyClusterRoleBinding(k8sclient.GetKubeClient().RbacV1(), roleBinding)
}

func (s *sdkAPIUpdater) applyStorageClass(sc *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	return applyStorageClass(k8sclient.GetKubeClient().StorageV1(), sc)
}

func (s *sdkAPIUpdater) applyDaemonSet(ds *appsv1.DaemonSet, expectedGeneration int64, forceRollout bool) (*appsv1.DaemonSet, bool, error) {
	return resourceapply.ApplyDaemonSet(k8sclient.GetKubeClient().AppsV1(), ds, expectedGeneration, forceRollout)
}

func (s *sdkAPIUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return k8sclient.
		GetKubeClient().StorageV1().
		StorageClasses().List(listOptions)
}

func (s *sdkAPIUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	s.recorder.Eventf(lv, eventType, reason, messageFmt)
}
