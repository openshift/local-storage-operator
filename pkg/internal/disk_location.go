package internal

// DiskLocation stores all tracked paths/details for a matched disk.
type DiskLocation struct {
	// DiskNamePath stores full device name path - "/dev/sda"
	DiskNamePath string
	// UserProvidedPath is the path supplied by the user in the LocalVolume CR.
	UserProvidedPath string
	DiskID           string
	BlockDevice      BlockDevice
	ForceWipe        bool
}
