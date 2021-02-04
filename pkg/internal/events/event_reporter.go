package events

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// ErrorListingExistingSymlinks is an event reason string
	ErrorListingExistingSymlinks = "ErrorListingExistingSymlinks"
	// DiscoveredNewDevice is an event reason string
	DiscoveredNewDevice = "DiscoveredNewDevice"
)

// KeyedEvent is a deduplicated type of event
// where an event is only reported once per key
type KeyedEvent interface {
	GetKey(string, string) string
	GetMessage() string
	GetEventReason() string
	GetEventType() string
}

// EventReporter for reporting events
type EventReporter struct {
	sync.Mutex
	Recorder       record.EventRecorder
	reportedEvents sets.String
}

// NewEventReporter returns an EventReporter
func NewEventReporter(eventRecorder record.EventRecorder) *EventReporter {
	er := &EventReporter{
		Recorder: eventRecorder,
	}
	er.reportedEvents = sets.NewString()
	return er
}

// ReportKeyedEvent reports a ReportKeyedEvent associated with an object after ensuring the event is not already reported
// by this instance of EventReporter. Which events count as the same is determined by the KeyedEvent's
// implementation of GetKey()
func (r *EventReporter) ReportKeyedEvent(obj runtime.Object, e KeyedEvent) error {
	r.Lock()
	defer r.Unlock()
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	name := accessor.GetName()
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	if len(name) == 0 || len(kind) == 0 {
		return fmt.Errorf("name: %q or kind: %q is empty for obj: %+v", name, kind, obj)
	}
	eventKey := e.GetKey(name, kind)
	if r.reportedEvents.Has(eventKey) {
		return nil
	}
	r.Recorder.Eventf(obj, e.GetEventType(), e.GetEventReason(), e.GetMessage())
	r.reportedEvents.Insert(eventKey)
	return nil
}
