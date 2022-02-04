package localvolume

import (
	"context"
	goctx "context"
	"fmt"
	"time"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	commontypes "github.com/openshift/local-storage-operator/common"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	apiTimeout = time.Minute
)

type apiUpdater interface {
	syncStatus(oldInstance, newInstance *localv1.LocalVolume) error
	updateLocalVolume(lv *localv1.LocalVolume) error
	applyStorageClass(ctx context.Context, required *storagev1.StorageClass) (*storagev1.StorageClass, bool, error)
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

func (s *sdkAPIUpdater) applyStorageClass(ctx context.Context, sc *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	return applyStorageClass(ctx, s.clientset.StorageV1(), sc)
}

func (s *sdkAPIUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return s.clientset.StorageV1().StorageClasses().List(goctx.Background(), listOptions)
}

func (s *sdkAPIUpdater) listPersistentVolumes(listOptions metav1.ListOptions) (*corev1.PersistentVolumeList, error) {
	return s.clientset.CoreV1().PersistentVolumes().List(goctx.Background(), listOptions)
}

func (s *sdkAPIUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	s.recorder.Eventf(lv, eventType, reason, messageFmt)
}

func (s *sdkAPIUpdater) apiContext() (goctx.Context, goctx.CancelFunc) {
	return goctx.WithTimeout(goctx.Background(), apiTimeout)
}
