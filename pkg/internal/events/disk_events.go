package events

import (
	"fmt"
	"os"
)

// NewDiskEvent returns a DiskEvent
func NewDiskEvent(eventReason, message, disk, eventType string) DiskEvent {
	return DiskEvent{
		NodeName:    os.Getenv("MY_NODE_NAME"),
		EventReason: eventReason,
		Message:     message,
		Disk:        disk,
		EventType:   eventType,
	}
}

// DiskEvent is instance of a single event associated with a disk (and possible a node)
type DiskEvent struct {
	NodeName    string
	EventType   string
	EventReason string
	Disk        string
	// The node name will be appended to this if the env var MY_NODE_NAME is set.
	Message string
}

// GetKey returns the deduplicationg key for DiskEvents
func (e DiskEvent) GetKey(objectName, objectKind string) string {
	// objectKind is not currently in the key, but if we ever use one eventreporter for two kinds,
	// we should add kind to the
	return fmt.Sprintf("%s:%s:%s:%s:%s", e.NodeName, objectName, e.EventReason, e.EventType, e.Disk)
}

// GetMessage returns the message for
func (e DiskEvent) GetMessage() string {
	message := e.Message
	if len(e.NodeName) != 0 {
		message = fmt.Sprintf("%s - %s", e.NodeName, message)
	}
	return message
}
