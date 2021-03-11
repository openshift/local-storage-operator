package events

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/clock"

	"k8s.io/client-go/tools/record"
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
	GetReason() string
	GetType() string
	// should return the interval after which the key can repeat
	GetInterval() time.Duration
}

// EventReporter for reporting events
type EventReporter struct {
	sync.Mutex
	Recorder       record.EventRecorder
	reportedEvents map[string]time.Time
	clock          clock.Clock
}

// NewEventReporter returns an EventReporter
func NewEventReporter(eventRecorder record.EventRecorder) *EventReporter {
	er := &EventReporter{
		Recorder: eventRecorder,
		clock:    clock.RealClock{},
	}
	er.reportedEvents = make(map[string]time.Time)
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
	lastReported, found := r.reportedEvents[eventKey]
	if !found || r.clock.Since(lastReported) >= e.GetInterval() {
		r.Recorder.Eventf(obj, e.GetType(), e.GetReason(), e.GetMessage())
		r.reportedEvents[eventKey] = r.clock.Now()
	}
	return nil
}
