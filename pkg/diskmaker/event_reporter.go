package diskmaker

import (
	"fmt"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	ErrorRunningBlockList    = "ErrorRunningBlockList"
	ErrorReadingBlockList    = "ErrorReadingBlockList"
	ErrorListingDeviceID     = "ErrorListingDeviceID"
	ErrorFindingMatchingDisk = "ErrorFindingMatchingDisk"
	ErrorCreatingSymLink     = "ErrorCreatingSymLink"

	FoundMatchingDisk = "FoundMatchingDisk"
)

type event struct {
	eventType   string
	eventReason string
	disk        string
	message     string
}

func newEvent(eventReason, message, disk string) *event {
	return &event{eventReason: eventReason, disk: disk, message: message, eventType: corev1.EventTypeWarning}
}

func newSuccessEvent(eventReason, message, disk string) *event {
	return &event{eventReason: eventReason, disk: disk, message: message, eventType: corev1.EventTypeNormal}
}

type eventReporter struct {
	apiClient      apiUpdater
	reportedEvents sets.String
}

func newEventReporter(apiClient apiUpdater) *eventReporter {
	er := &eventReporter{apiClient: apiClient}
	er.reportedEvents = sets.NewString()
	return er
}

// report function is not thread safe
func (reporter *eventReporter) report(e *event, lv *localv1.LocalVolume) {
	eventKey := fmt.Sprintf("%s:%s:%s", e.eventReason, e.eventType, e.disk)
	if reporter.reportedEvents.Has(eventKey) {
		return
	}

	reporter.apiClient.recordEvent(lv, e)
	reporter.reportedEvents.Insert(eventKey)
}
