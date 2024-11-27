package common

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"syscall"

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
		if errors.Is(err, os.ErrNotExist) {
			// If the symlink was already deleted, we may still
			// need to remove the parent directory.
			return deleteSymlinkParentDir(symlink)
		}
		return fmt.Errorf("failed to lstat %s: %v", symlink, err)
	}
	if fileInfo.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("not removing %s: file is not a symlink", symlink)
	}

	// Remove symlink
	err = os.Remove(symlink)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("error removing symlink %s: %v", symlink, err)
	}

	// Remove the parent directory if this was the last remaining symlink
	return deleteSymlinkParentDir(symlink)
}

// deleteSymlinkParentDir tries to remove the parent directory of the symlink.
// Don't bother with extra syscalls to check if the directory is empty first,
// just attempt to remove the directory and return nil if we get ENOTEMPTY.
// The last symlink to be removed will remove the parent directory with it.
func deleteSymlinkParentDir(symlink string) error {
	parentDir := filepath.Dir(symlink)
	err := os.Remove(parentDir)
	if err != nil {
		// Quietly return success if the directory was already
		// deleted, or if the directory is not yet empty.
		if errors.Is(err, os.ErrNotExist) ||
			errors.Is(err, syscall.ENOTEMPTY) {
			return nil
		}
		return fmt.Errorf("error removing symlink dir %s: %v", parentDir, err)
	}
	klog.Infof("removed empty symlink directory %s", parentDir)
	return nil
}

func pvHasLabels(pv *corev1.PersistentVolume, labels map[string]string) bool {
	for key, value := range labels {
		v, found := pv.Labels[key]
		if !found || v != value {
			return false
		}
	}
	return true
}

type ConditionalSymlinkRemoval func() bool

// CleanupSymlinks processes deleted PersistentVolumes with the
// storage.openshift.com/lso-symlink-deleter finalizer, removes the symlink,
// and then removes the finalizer to allow the PV deletion to complete.
// The ownerLabels arg allows the caller to only process PV's with those labels.
// The shouldDeleteSymlinkFnArg callback allows the caller to selectively remove
// symlinks for a subset of PV's but still remove the finalizer for all of them.
func CleanupSymlinks(c client.Client, r *provCommon.RuntimeConfig, ownerLabels map[string]string, shouldDeleteSymlinkFnArg ConditionalSymlinkRemoval) error {
	// If shouldDeleteSymlinkFnArg is nil, default to always returning true.
	var shouldDeleteSymlinkFn ConditionalSymlinkRemoval
	if shouldDeleteSymlinkFnArg == nil {
		shouldDeleteSymlinkFn = func() bool { return true }
	} else {
		shouldDeleteSymlinkFn = shouldDeleteSymlinkFnArg
	}

	for _, pv := range r.Cache.ListPVs() {
		// Only process PV's with the provided owner labels.
		if !pvHasLabels(pv, ownerLabels) {
			continue
		}
		// Only process deleted volumes
		if pv.DeletionTimestamp.IsZero() {
			continue
		}
		// Only process Released or Available volumes.
		// LSO will release all available PV's if the LV or LVS
		// is deleted, but we still need to cleanup and remove
		// the finalizer if a user deletes an available PV.
		if pv.Status.Phase != corev1.VolumeReleased &&
			pv.Status.Phase != corev1.VolumeAvailable {
			continue
		}
		if !hasSymlinkFinalizer(pv) {
			continue
		}
		if shouldDeleteSymlinkFn() {
			err := deleteSymlink(pv)
			if err != nil {
				return err
			}
		}
		err := removeSymlinkFinalizer(c, pv)
		if err != nil {
			return err
		}
	}
	return nil
}
