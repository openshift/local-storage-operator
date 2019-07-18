package diskmaker

import (
	"fmt"
	"os"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

type apiUpdater interface {
	recordEvent(lv *localv1.LocalVolume, e *event)
	getLocalVolume(lv *localv1.LocalVolume) (*localv1.LocalVolume, error)
}

type sdkAPIUpdater struct {
	recorder record.EventRecorder
}

func newAPIUpdater() apiUpdater {
	apiClient := &sdkAPIUpdater{}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(k8sclient.GetKubeClient().CoreV1().RESTClient()).Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "local-storage-diskmaker"})
	apiClient.recorder = recorder
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
	err := sdk.Get(lv)
	return lv, err
}
