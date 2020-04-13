package controller

import (
	goctx "context"
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	extscheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/kubernetes/scheme"
	cgoscheme "k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
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
	recorder  record.EventRecorder
	dynClient dynclient.Client
}

func newAPIUpdater(namespace string) (apiUpdater, error) {
	apiClient := &sdkAPIUpdater{}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(k8sclient.GetKubeClient().CoreV1().RESTClient()).Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: componentName})
	apiClient.recorder = recorder
	err := apiClient.setupDynamicClient(namespace)
	if err != nil {
		return apiClient, err
	}
	return apiClient, nil
}

var _ apiUpdater = &sdkAPIUpdater{}

func (s *sdkAPIUpdater) updateLocalVolume(lv *localv1.LocalVolume) error {
	klog.Infof("Updating localvolume %s", commontypes.LocalVolumeKey(lv))
	err := sdk.Update(lv)
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
		err := s.dynClient.Status().Update(ctx, newInstance)
		if err != nil && errors.IsConflict(err) {
			msg := fmt.Sprintf("updating localvolume %s failed: %v", commontypes.LocalVolumeKey(newInstance), err)
			s.recordEvent(newInstance, corev1.EventTypeWarning, localVolumeUpdateFailed, msg)
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
	if forceRollout {
		klog.Infof("Rolling out DaemonSet: %s/%s", ds.Name, ds.Namespace)
	}
	return resourceapply.ApplyDaemonSet(k8sclient.GetKubeClient().AppsV1(), ds, expectedGeneration, forceRollout)
}

func (s *sdkAPIUpdater) getDaemonSet(namespace, dsName string) (*appsv1.DaemonSet, error) {
	return k8sclient.GetKubeClient().AppsV1().DaemonSets(namespace).Get(dsName, metav1.GetOptions{})
}

func (s *sdkAPIUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return k8sclient.
		GetKubeClient().StorageV1().
		StorageClasses().List(listOptions)
}

func (s *sdkAPIUpdater) listPersistentVolumes(listOptions metav1.ListOptions) (*corev1.PersistentVolumeList, error) {
	return k8sclient.GetKubeClient().CoreV1().PersistentVolumes().List(listOptions)
}

func (s *sdkAPIUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	s.recorder.Eventf(lv, eventType, reason, messageFmt)
}

// initialize controller-runtime dynamic client so as we can write status subresoure
func (s *sdkAPIUpdater) setupDynamicClient(namespace string) error {
	scheme := runtime.NewScheme()
	cgoscheme.AddToScheme(scheme)
	extscheme.AddToScheme(scheme)
	err := localv1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("error adding localvolume to scheme: %v", err)
	}

	cachedDiscoveryClient := cached.NewMemCacheClient(k8sclient.GetKubeClient().Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscoveryClient)
	restMapper.Reset()
	dynClient, err := dynclient.New(k8sclient.GetKubeConfig(), dynclient.Options{Scheme: scheme, Mapper: restMapper})
	if err != nil {
		return fmt.Errorf("error initializing dynamic client: %v", err)
	}
	localVolumeList := &localv1.LocalVolumeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.SchemeGroupVersion.String(),
		},
	}

	err = wait.PollImmediate(time.Second, time.Second*10, func() (done bool, err error) {
		err = dynClient.List(goctx.TODO(), &dynclient.ListOptions{Namespace: namespace}, localVolumeList)
		if err != nil {
			restMapper.Reset()
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("failed to build dynamic client: %v", err)
	}
	s.dynClient = dynClient
	return nil
}

func (s *sdkAPIUpdater) apiContext() (goctx.Context, goctx.CancelFunc) {
	return goctx.WithTimeout(goctx.Background(), apiTimeout)
}
