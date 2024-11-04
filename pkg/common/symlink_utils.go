package common

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

func hasSymlinkFinalizer(pv *corev1.PersistentVolume) bool {
	return controllerutil.ContainsFinalizer(pv, LSOSymlinkDeleterFinalizer)
}

func removeSymlinkFinalizer(c client.Client, pv *corev1.PersistentVolume) error {
	pvCopy := pv.DeepCopy()
	klog.Infof("removing finalizer from PV %s", pvCopy.Name)
	if controllerutil.RemoveFinalizer(pvCopy, LSOSymlinkDeleterFinalizer) {
		err := c.Update(context.TODO(), pvCopy)
		if err != nil {
			return fmt.Errorf("error removing %s finalizer from PV %s: %v", LSOSymlinkDeleterFinalizer, pvCopy.Name, err)
		}
	}
	return nil
}

func deleteSymlink(pv *corev1.PersistentVolume) error {
	if pv.DeletionTimestamp.IsZero() {
		return fmt.Errorf("failed to remove symlink, PV %s does not have DeletionTimestamp", pv.Name)
	}
	if pv.Spec.PersistentVolumeSource.Local == nil {
		return fmt.Errorf("failed to remove symlink, PV %s does not have a local volume path", pv.Name)
	}
	symlink := pv.Spec.PersistentVolumeSource.Local.Path
	klog.Infof("removing symlink %s", symlink)

	// Check if file exists and is a symlink
	fileInfo, err := os.Lstat(symlink)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to lstat %s: %v", symlink, err)
	}
	if fileInfo.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("not removing %s: file is not a symlink", symlink)
	}

	// Remove symlink
	err = os.Remove(symlink)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing symlink %s: %v", symlink, err)
	}

	return nil
}

// CleanupSymlinks processes deleted PersistentVolumes with the
// storage.openshift.com/lso-symlink-deleter finalizer, removes the symlink,
// and then removes the finalizer to allow the PV deletion to complete.
func CleanupSymlinks(c client.Client, r *provCommon.RuntimeConfig) error {
	for _, pv := range r.Cache.ListPVs() {
		if pv.Status.Phase != corev1.VolumeReleased {
			continue
		}
		if pv.DeletionTimestamp.IsZero() {
			continue
		}
		if !hasSymlinkFinalizer(pv) {
			continue
		}
		err := deleteSymlink(pv)
		if err != nil {
			return err
		}
		err = removeSymlinkFinalizer(c, pv)
		if err != nil {
			return err
		}
	}
	return nil
}
