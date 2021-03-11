package events

import (
	"fmt"
	"os"
	"time"
)

const (
	// LocalVolume events
	ErrorRunningBlockList    = "ErrorRunningBlockList"
	ErrorReadingBlockList    = "ErrorReadingBlockList"
	ErrorListingDeviceID     = "ErrorListingDeviceID"
	ErrorFindingMatchingDisk = "ErrorFindingMatchingDisk"
	SymLinkedOnDeviceName    = "SymlinkedOnDeivceName"
	ErrorProvisioningDisk    = "ErrorProvisioningDisk"

	FoundMatchingDisk   = "FoundMatchingDisk"
	DeviceSymlinkExists = "DeviceSymlinkExists"

	// LocalVolumeDiscovery events
	ErrorCreatingDiscoveryResultObject = "ErrorCreatingDiscoveryResultObject"
	ErrorUpdatingDiscoveryResultObject = "ErrorUpdatingDiscoveryResultObject"
	ErrorListingBlockDevices           = "ErrorListingBlockDevices"

	CreatedDiscoveryResultObject = "CreatedDiscoveryResultObject"
	UpdatedDiscoveredDeviceList  = "UpdatedDiscoveredDeviceList"
)

// NewDiskEvent returns a DiskEvent with a default interval of 30 minutes
func NewDiskEvent(eventReason, message, disk, eventType string) DiskEvent {
	return DiskEvent{
		NodeName:    os.Getenv("MY_NODE_NAME"),
		EventType:   eventType,
		EventReason: eventReason,
		Message:     message,
		Disk:        disk,
		Interval:    time.Minute * 30,
	}
}

// DiskEvent is instance of a single event associated with a disk (and possible a node)
type DiskEvent struct {
	NodeName    string
	EventType   string
	EventReason string
	Disk        string
	// The node name will be appended to this if the env var MY_NODE_NAME is set.
	Message  string
	Interval time.Duration
}

// GetInterval returns the duration before a key is allowed to repeat
func (e DiskEvent) GetInterval() time.Duration {
	return e.Interval
}

// GetKey returns the deduplicationg key for DiskEvents
func (e DiskEvent) GetKey(objectName, objectKind string) string {
	// objectKind is not currently in the key, but if we ever use one eventreporter for two kinds,
	// we should add kind to the key
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

// GetReason returns the event reason
func (e DiskEvent) GetReason() string {
	return e.EventReason
}

// GetType returns the event reason
func (e DiskEvent) GetType() string {
	return e.EventType
}
