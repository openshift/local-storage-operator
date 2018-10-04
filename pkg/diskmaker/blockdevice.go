package diskmaker

// Block device
type BlockDevice struct {
	Name       string `json:"name"`
	DiskType   string `json:"type"`
	Size       string `json:"size"`
	MountPoint string `json:"mountpoint"`
}

type DeviceArray []BlockDevice
type BlockDeviceMap map[string]DeviceArray
