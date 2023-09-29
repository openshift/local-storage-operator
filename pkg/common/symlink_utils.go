package common

import (
	"errors"
	"path"
	"path/filepath"

	"github.com/openshift/local-storage-operator/pkg/internal"
)

// GetSymLinkSourceAndTarget returns
// `source`: the /dev/disk/by-id path of the device if it exists, /dev/KNAME if it doesn't
// `target`: the path in the symlinkdir to symlink to. device-id if it exists, KNAME if it doesn't
// `idExists`: is set if the device-id exists
// `err`
func GetSymLinkSourceAndTarget(dev internal.BlockDevice, symlinkDir string) (string, string, bool, error) {
	var source string
	var target string
	var idExists = true
	var err error

	// get /dev/KNAME path
	devLabelPath, err := dev.GetDevPath()
	if err != nil {
		return source, target, false, err
	}
	// determine symlink source
	source, err = dev.GetPathByID()
	if errors.As(err, &internal.IDPathNotFoundError{}) {
		// no disk-by-id
		idExists = false
		source = devLabelPath
	} else if err != nil {
		return source, target, false, err
	}
	target = path.Join(symlinkDir, filepath.Base(source))
	return source, target, idExists, nil

}
