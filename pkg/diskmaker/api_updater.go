package diskmaker

import (
	"context"
	"fmt"
	"os"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/prometheus/common/log"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const componentName = "local-storage-diskmaker"

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

func newAPIUpdater(scheme *runtime.Scheme) (apiUpdater, error) {

	recorder, err := getEventRecorder(scheme)
	if err != nil {
		log.Error(err, "failed to get event recorder")
		return &sdkAPIUpdater{}, err
	}

	config, err := config.GetConfig()
	if err != nil {
		log.Error(err, "failed to get rest.config")
		return &sdkAPIUpdater{}, err
	}
	crClient, err := client.New(config, client.Options{})
	if err != nil {
		log.Error(err, "failed to create controller-runtime client")
		return &sdkAPIUpdater{}, err
	}

	apiClient := &sdkAPIUpdater{
		client:   crClient,
		recorder: recorder,
	}
	return apiClient, nil
}

func getEventRecorder(scheme *runtime.Scheme) (record.EventRecorder, error) {
	var recorder record.EventRecorder
	config, err := config.GetConfig()
	if err != nil {
		log.Error(err, "failed to get rest.config")
		return recorder, err
	}
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error(err, "could not build kubeclient")
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder = eventBroadcaster.NewRecorder(scheme, v1.EventSource{Component: componentName})
	return recorder, nil
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
