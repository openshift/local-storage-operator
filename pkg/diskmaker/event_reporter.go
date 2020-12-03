package diskmaker

import (
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// LocalVolume events
	ErrorRunningBlockList    = "ErrorRunningBlockList"
	ErrorReadingBlockList    = "ErrorReadingBlockList"
	ErrorListingDeviceID     = "ErrorListingDeviceID"
	ErrorFindingMatchingDisk = "ErrorFindingMatchingDisk"
	ErrorCreatingSymLink     = "ErrorCreatingSymLink"
	SymLinkedOnDeviceName    = "SymlinkedOnDeivceName"

	FoundMatchingDisk   = "FoundMatchingDisk"
	DeviceSymlinkExists = "DeviceSymlinkExists"

	// LocalVolumeDiscovery events
	ErrorCreatingDiscoveryResultObject = "ErrorCreatingDiscoveryResultObject"
	ErrorUpdatingDiscoveryResultObject = "ErrorUpdatingDiscoveryResultObject"
	ErrorListingBlockDevices           = "ErrorListingBlockDevices"

	CreatedDiscoveryResultObject = "CreatedDiscoveryResultObject"
	UpdatedDiscoveredDeviceList  = "UpdatedDiscoveredDeviceList"
)

// DiskEvent is instance of a single event
type DiskEvent struct {
	EventType   string
	EventReason string
	Disk        string
	Message     string
}

// NewEvent returns a new disk event of type warning
func NewEvent(eventReason, message, disk string) *DiskEvent {
	return &DiskEvent{EventReason: eventReason, Disk: disk, Message: message, EventType: corev1.EventTypeWarning}
}

// NewSuccessEvent returns a normal event type
func NewSuccessEvent(eventReason, message, disk string) *DiskEvent {
	return &DiskEvent{EventReason: eventReason, Disk: disk, Message: message, EventType: corev1.EventTypeNormal}
}

// EventReporter instance
type EventReporter struct {
	mux            sync.Mutex
	apiClient      ApiUpdater
	reportedEvents sets.String
}

// NewEventReporter returns a new event reportor
func NewEventReporter(apiClient ApiUpdater) *EventReporter {
	er := &EventReporter{apiClient: apiClient}
	er.reportedEvents = sets.NewString()
	return er
}

// Report an event
func (reporter *EventReporter) Report(e *DiskEvent, obj runtime.Object) {
	reporter.mux.Lock()
	defer reporter.mux.Unlock()
	eventKey := fmt.Sprintf("%s:%s:%s", e.EventReason, e.EventType, e.Disk)
	if reporter.reportedEvents.Has(eventKey) {
		return
	}

	reporter.apiClient.recordEvent(obj, e)
	reporter.reportedEvents.Insert(eventKey)
}
