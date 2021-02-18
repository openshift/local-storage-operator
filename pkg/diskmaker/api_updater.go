package diskmaker

import (
	"context"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/internal/events"
	"github.com/prometheus/common/log"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const componentName = "local-storage-diskmaker"

type ApiUpdater interface {
	RecordKeyedEvent(obj runtime.Object, e events.KeyedEvent)
	getLocalVolume(lv *localv1.LocalVolume) (*localv1.LocalVolume, error)
	CreateDiscoveryResult(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error
	GetDiscoveryResult(name, namespace string) (*v1alpha1.LocalVolumeDiscoveryResult, error)
	UpdateDiscoveryResultStatus(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error
	UpdateDiscoveryResult(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error
	GetLocalVolumeDiscovery(name, namespace string) (*v1alpha1.LocalVolumeDiscovery, error)
}

type sdkAPIUpdater struct {
	EventReporter *events.EventReporter
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
}

func NewAPIUpdater(scheme *runtime.Scheme) (ApiUpdater, error) {
	recorder, err := getEventRecorder(scheme)
	if err != nil {
		log.Error(err, "failed to get event recorder")
		return &sdkAPIUpdater{}, err
	}
	reporter := events.NewEventReporter(recorder)
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
		client:        crClient,
		EventReporter: reporter,
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

func (s *sdkAPIUpdater) RecordKeyedEvent(obj runtime.Object, e events.KeyedEvent) {
	s.EventReporter.ReportKeyedEvent(obj, e)
}

func (s *sdkAPIUpdater) getLocalVolume(lv *localv1.LocalVolume) (*localv1.LocalVolume, error) {
	newLocalVolume := lv.DeepCopy()
	err := s.client.Get(context.TODO(), types.NamespacedName{Name: newLocalVolume.GetName(), Namespace: newLocalVolume.GetNamespace()}, newLocalVolume)
	return lv, err
}

func (s *sdkAPIUpdater) GetDiscoveryResult(name, namespace string) (*v1alpha1.LocalVolumeDiscoveryResult, error) {
	discoveryResult := &v1alpha1.LocalVolumeDiscoveryResult{}
	err := s.client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, discoveryResult)
	return discoveryResult, err
}

func (s *sdkAPIUpdater) CreateDiscoveryResult(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error {
	return s.client.Create(context.TODO(), lvdr)
}

func (s *sdkAPIUpdater) UpdateDiscoveryResultStatus(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error {
	return s.client.Status().Update(context.TODO(), lvdr)
}

func (s *sdkAPIUpdater) UpdateDiscoveryResult(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error {
	return s.client.Update(context.TODO(), lvdr)
}

func (s *sdkAPIUpdater) GetLocalVolumeDiscovery(name, namespace string) (*v1alpha1.LocalVolumeDiscovery, error) {
	discoveryCR := &v1alpha1.LocalVolumeDiscovery{}
	err := s.client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, discoveryCR)
	return discoveryCR, err
}
