package events

import (
	"fmt"
	"time"
)

// NewReconcileEvent returns a DiskEvent
func NewReconcileEvent(eventReason, message, eventType string) ReconcileEvent {
	return ReconcileEvent{
		Reason:   eventReason,
		Message:  message,
		Type:     eventType,
		Interval: time.Minute * 30,
	}
}

// ReconcileEvent is instance of a single event associated with a controller's reconcile loop
type ReconcileEvent struct {
	Type     string
	Reason   string
	Message  string
	Interval time.Duration
}

// GetInterval returns the duration before a key is allowed to repeat
func (e ReconcileEvent) GetInterval() time.Duration {
	return e.Interval
}

// GetKey returns the deduplicationg key for ReconcileEvents
func (e ReconcileEvent) GetKey(objectName, objectKind string) string {
	// objectKind is not currently in the key, but if we ever use one eventreporter for two kinds,
	// we should add kind to the key
	return fmt.Sprintf("%s:%s:%s", objectName, e.Reason, e.Type)
}

// GetMessage returns the message for
func (e ReconcileEvent) GetMessage() string {
	return e.Message
}

// GetReason returns the event reason
func (e ReconcileEvent) GetReason() string {
	return e.Reason
}

// GetType returns the event reason
func (e ReconcileEvent) GetType() string {
	return e.Type
}
