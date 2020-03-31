package diskmaker

import (
	"context"
	"fmt"
	"os"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type apiUpdater interface {
	recordEvent(lv *localv1.LocalVolume, e *event)
	getLocalVolume(lv *localv1.LocalVolume) (*localv1.LocalVolume, error)
}

type sdkAPIUpdater struct {
	recorder record.EventRecorder
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
}

func newAPIUpdater(mgr manager.Manager) apiUpdater {
	apiClient := &sdkAPIUpdater{
		client:   mgr.GetClient(),
		recorder: mgr.GetEventRecorderFor("local-storage-diskmaker"),
	}
	return apiClient
}

func (s *sdkAPIUpdater) recordEvent(lv *localv1.LocalVolume, e *event) {
	nodeName := os.Getenv("MY_NODE_NAME")
	message := e.message
	if len(nodeName) != 0 {
		message = fmt.Sprintf("%s - %s", nodeName, message)
	}

	s.recorder.Eventf(lv, e.eventType, e.eventReason, message)
}

func (s *sdkAPIUpdater) getLocalVolume(lv *localv1.LocalVolume) (*localv1.LocalVolume, error) {
	newLocalVolume := lv.DeepCopy()
	err := s.client.Get(context.TODO(), types.NamespacedName{Name: newLocalVolume.GetName(), Namespace: newLocalVolume.GetNamespace()}, newLocalVolume)
	return lv, err
}
