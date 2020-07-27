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

type DiskEvent struct {
	EventType   string
	EventReason string
	Disk        string
	Message     string
}

func newEvent(eventReason, message, disk string) *DiskEvent {
	return &DiskEvent{EventReason: eventReason, Disk: disk, Message: message, EventType: corev1.EventTypeWarning}
}

func newSuccessEvent(eventReason, message, disk string) *DiskEvent {
	return &DiskEvent{EventReason: eventReason, Disk: disk, Message: message, EventType: corev1.EventTypeNormal}
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
func (reporter *eventReporter) report(e *DiskEvent, lv *localv1.LocalVolume) {
	eventKey := fmt.Sprintf("%s:%s:%s", e.EventReason, e.EventType, e.Disk)
	if reporter.reportedEvents.Has(eventKey) {
		return
	}

	reporter.apiClient.recordEvent(lv, e)
	reporter.reportedEvents.Insert(eventKey)
}
