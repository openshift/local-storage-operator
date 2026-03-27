package internal

// DiskLocation stores all tracked paths/details for a matched disk.
type DiskLocation struct {
	// DiskNamePath stores full device name path - "/dev/sda"
	DiskNamePath string
	// UserProvidedPath is the path supplied by the user in the LocalVolume CR. Empty in LocalVolumeSet
	UserProvidedPath string
	DiskID           string
	BlockDevice      BlockDevice
	ForceWipe        bool

	// provisioning related fields set for later

	// SymlinkPath represents path in /mnt/local-storage directory
	SymlinkPath string
	//SymlinkSource represents path in /dev filesystem
	SymlinkSource string
	//ByIDPathExists is set if a valid path is found in /dev/disk/by-id
	ByIDPathExists bool
}
