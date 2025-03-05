package localvolumeset

import (
	"context"
	goctx "context"
	"time"

	"github.com/openshift/local-storage-operator/pkg/common"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	apiTimeout = time.Minute
)

type apiUpdater interface {
	applyStorageClass(ctx context.Context, required *storagev1.StorageClass) (*storagev1.StorageClass, bool, error)
	listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error)
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
		recorder:  mgr.GetEventRecorderFor(ComponentName),
		client:    mgr.GetClient(),
		clientset: clientset,
	}
	return apiClient
}

var _ apiUpdater = &sdkAPIUpdater{}

func (s *sdkAPIUpdater) applyStorageClass(ctx context.Context, sc *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	return common.ApplyStorageClass(ctx, s.clientset.StorageV1(), sc)
}

func (s *sdkAPIUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return s.clientset.StorageV1().StorageClasses().List(goctx.Background(), listOptions)
}
