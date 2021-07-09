package common

import (
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/openshift/local-storage-operator/internal"
	"github.com/prometheus/common/log"
	v1 "k8s.io/api/core/v1"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
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

func GetCleanPVSymlinkFunc(runtimeConfig *provCommon.RuntimeConfig) func(pv *v1.PersistentVolume) error {
	return func(pv *v1.PersistentVolume) error {
		log.Infof("Removing symlink for %s", pv.ObjectMeta.Name)
		config, ok := runtimeConfig.DiscoveryMap[pv.Spec.StorageClassName]
		if !ok {
			return fmt.Errorf("Unknown storage class name %s", pv.Spec.StorageClassName)
		}
		mountPath, err := provCommon.GetContainerPath(pv, config)
		if err != nil {
			return fmt.Errorf("Unable to get mountPath: %w", err)
		}
		cmd := exec.Command("rm", mountPath)
		err = cmd.Start()
		if err != nil {
			return err
		}
		return nil
	}
}
