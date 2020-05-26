package localvolume

import (
	"context"
	goctx "context"
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	apiTimeout = time.Minute
)

type apiUpdater interface {
	syncStatus(oldInstance, newInstance *localv1.LocalVolume) error
	updateLocalVolume(lv *localv1.LocalVolume) error
	applyServiceAccount(serviceAccount *corev1.ServiceAccount) (*corev1.ServiceAccount, bool, error)
	applyConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, bool, error)
	applyClusterRole(clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error)
	applyClusterRoleBinding(roleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error)
	applyRole(role *rbacv1.Role) (*rbacv1.Role, bool, error)
	applyRoleBinding(roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error)
	applyStorageClass(required *storagev1.StorageClass) (*storagev1.StorageClass, bool, error)
	applyDaemonSet(ds *appsv1.DaemonSet, expectedGeneration int64, forceRollout bool) (*appsv1.DaemonSet, bool, error)
	getDaemonSet(namespace, dsName string) (*appsv1.DaemonSet, error)
	listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error)
	listPersistentVolumes(listOptions metav1.ListOptions) (*corev1.PersistentVolumeList, error)
	recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{})
}

type sdkAPIUpdater struct {
	recorder record.EventRecorder
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	clientset kubernetes.Interface
}

func newAPIUpdater(mgr manager.Manager) apiUpdater {
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		panic(err)
	}
	apiClient := &sdkAPIUpdater{
		recorder:  mgr.GetEventRecorderFor(componentName),
		client:    mgr.GetClient(),
		clientset: clientset,
	}
	return apiClient
}

var _ apiUpdater = &sdkAPIUpdater{}

func (s *sdkAPIUpdater) updateLocalVolume(lv *localv1.LocalVolume) error {
	klog.Infof("Updating localvolume %s", commontypes.LocalVolumeKey(lv))
	err := s.client.Update(context.TODO(), lv)
	if err != nil && errors.IsConflict(err) {
		msg := fmt.Sprintf("updating localvolume %s failed: %v", commontypes.LocalVolumeKey(lv), err)
		s.recordEvent(lv, corev1.EventTypeWarning, localVolumeUpdateFailed, msg)
	}
	return err
}

func (s *sdkAPIUpdater) syncStatus(oldInstance, newInstance *localv1.LocalVolume) error {
	klog.V(4).Infof("Syncing LocalVolume.Status of %s", commontypes.LocalVolumeKey(newInstance))

	if !equality.Semantic.DeepEqual(oldInstance.Status, newInstance.Status) {
		klog.V(4).Infof("Updating LocalVolume.Status of %s", commontypes.LocalVolumeKey(newInstance))
		ctx, cancel := s.apiContext()
		defer cancel()
		err := s.client.Status().Update(ctx, newInstance)
		if err != nil && errors.IsConflict(err) {
			msg := fmt.Sprintf("updating localvolume %s failed: %v", commontypes.LocalVolumeKey(newInstance), err)
			s.recordEvent(newInstance, corev1.EventTypeWarning, localVolumeUpdateFailed, msg)
		}
		return err
	}
	return nil
}

func (s *sdkAPIUpdater) applyServiceAccount(sa *corev1.ServiceAccount) (*corev1.ServiceAccount, bool, error) {
	return resourceapply.ApplyServiceAccount(s.clientset.CoreV1(), events.NewInMemoryRecorder(componentName), sa)
}

func (s *sdkAPIUpdater) applyConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, bool, error) {
	return resourceapply.ApplyConfigMap(s.clientset.CoreV1(), events.NewInMemoryRecorder(componentName), configmap)
}

func (s *sdkAPIUpdater) applyRole(role *rbacv1.Role) (*rbacv1.Role, bool, error) {
	return resourceapply.ApplyRole(s.clientset.RbacV1(), events.NewInMemoryRecorder(componentName), role)
}

func (s *sdkAPIUpdater) applyRoleBinding(roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error) {
	return resourceapply.ApplyRoleBinding(s.clientset.RbacV1(), events.NewInMemoryRecorder(componentName), roleBinding)
}

func (s *sdkAPIUpdater) applyClusterRole(clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error) {
	return resourceapply.ApplyClusterRole(s.clientset.RbacV1(), events.NewInMemoryRecorder(componentName), clusterRole)
}

func (s *sdkAPIUpdater) applyClusterRoleBinding(roleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error) {
	return resourceapply.ApplyClusterRoleBinding(s.clientset.RbacV1(), events.NewInMemoryRecorder(componentName), roleBinding)
}

func (s *sdkAPIUpdater) applyStorageClass(sc *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	return applyStorageClass(s.clientset.StorageV1(), sc)
}

func (s *sdkAPIUpdater) applyDaemonSet(ds *appsv1.DaemonSet, expectedGeneration int64, forceRollout bool) (*appsv1.DaemonSet, bool, error) {
	if forceRollout {
		klog.Infof("Rolling out DaemonSet: %s/%s", ds.Name, ds.Namespace)
	}
	return resourceapply.ApplyDaemonSet(s.clientset.AppsV1(), events.NewInMemoryRecorder(componentName), ds, expectedGeneration, forceRollout)
}

func (s *sdkAPIUpdater) getDaemonSet(namespace, dsName string) (*appsv1.DaemonSet, error) {
	return k8sclient.GetKubeClient().AppsV1().DaemonSets(namespace).Get(dsName, metav1.GetOptions{})
}

func (s *sdkAPIUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return s.clientset.StorageV1().StorageClasses().List(listOptions)
}

func (s *sdkAPIUpdater) listPersistentVolumes(listOptions metav1.ListOptions) (*corev1.PersistentVolumeList, error) {
	return s.clientset.CoreV1().PersistentVolumes().List(listOptions)
}

func (s *sdkAPIUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	s.recorder.Eventf(lv, eventType, reason, messageFmt)
}

func (s *sdkAPIUpdater) apiContext() (goctx.Context, goctx.CancelFunc) {
	return goctx.WithTimeout(goctx.Background(), apiTimeout)
}
