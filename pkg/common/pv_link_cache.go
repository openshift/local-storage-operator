package common

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sync"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// LocalVolumeDeviceLinkCache maintains an in-memory index of LocalVolumeDeviceLink
// objects keyed by their valid link targets (e.g. /dev/disk/by-id paths). Reconcilers
// use this cache to recover device-to-PV associations when the on-disk symlink has
// been lost (e.g. after a node reboot changes /dev/disk/by-id entries), enabling
// symlink recreation without re-provisioning the PersistentVolume.
type LocalVolumeDeviceLinkCache struct {
	mu     sync.RWMutex
	client client.Client
	mgr    manager.Manager

	synced chan struct{}
	// map of symlinkName in /dev/disk/byid and CurrentBlockDeviceInfo
	localDeviceInfos map[string]CurrentBlockDeviceInfo
}

type CurrentBlockDeviceInfo struct {
	// map of lvdlname and LocalVolumeDeviceLink
	lvdls map[string]*v1.LocalVolumeDeviceLink
}

// GetSymlinkTargetPath returns a symlinkPath in /mnt/local-storage for Local volumes.
// The purpose of this function is to return a new path which may or may not match with
// actual source specified by newSymlinkSourcePath for existing symlinks.
// For example - /dev/disk/by-id/wwn-0x12232 newSymlinkSourcePath may generate a targetpath
// called /mnt/local-storage/foobar/scsi-23232 because scsi-xxx path is already being used
// by existing volumes.
//
// Arguments:
// symlinkDir is path in /mnt/local-storage with storageclass name:
//
//	example - /mnt/local-storage/foobar/
//
// newSymlinkSourcePath is path in /dev/disk/by-id which points to current device:
//
//	example - /dev/disk/by-id/wwn-0x123432
//
// Assuming existing currentLinkTarget somehow doesn't resolve, we could be in this code path.
// Only return valid new SymlinkPath if currentLinkTarget doesn't resolve and user has asked
// for symlinks to be recreated.
func (c CurrentBlockDeviceInfo) GetSymlinkTargetPath(ctx context.Context, symlinkDir, newSymlinkSourcePath string, client client.Client) (string, error) {
	lvdls := c.lvdls
	if len(lvdls) > 1 {
		return "", fmt.Errorf("more than one LocalVolumeDevicelink found for %s path", newSymlinkSourcePath)
	}

	// this should NEVER happen, because we always insert an lvdl objects
	if len(lvdls) == 0 {
		return "", fmt.Errorf("unexpected empty lvdl set for symlink %s", newSymlinkSourcePath)
	}

	lvdl, pv, err := c.getLVDLAndPV(ctx, client)
	if err != nil {
		return "", err
	}
	currentLinkTarget := lvdl.Status.CurrentLinkTarget
	validLinkTargets := lvdl.Status.ValidLinkTargets
	policy := lvdl.Spec.Policy

	if policy != v1.DeviceLinkPolicyPreferredLinkTarget {
		return "", fmt.Errorf("found stale symlink link for %s in %s", newSymlinkSourcePath, symlinkDir)
	}

	// check if currentLinkTarget resolves to a valid device, if yes then no need to do anything
	resolvedCurrent, err := internal.FilePathEvalSymLinks(currentLinkTarget)
	if err == nil {
		return "", fmt.Errorf("currentSymlink %s still resolves to %s for %s", currentLinkTarget, resolvedCurrent, newSymlinkSourcePath)
	}

	if pv.Spec.Local == nil || pv.Spec.Local.Path == "" {
		return "", fmt.Errorf("pv %s has empty local path", pv.Name)
	}
	currentTargetPath := pv.Spec.Local.Path
	symlinkBaseName := filepath.Base(currentTargetPath)

	if slices.Contains(validLinkTargets, newSymlinkSourcePath) {
		return filepath.Join(symlinkDir, symlinkBaseName), nil
	}
	klog.Warningf("symlink source %s is not recorded in valid symlink target, but has stale PVs that use the device", newSymlinkSourcePath)
	return filepath.Join(symlinkDir, symlinkBaseName), nil
}

func (c CurrentBlockDeviceInfo) getLVDLAndPV(ctx context.Context, client client.Client) (*v1.LocalVolumeDeviceLink, *corev1.PersistentVolume, error) {
	var lvdl *v1.LocalVolumeDeviceLink
	for _, v := range c.lvdls {
		lvdl = v
		break
	}
	pv := &corev1.PersistentVolume{}
	if err := client.Get(ctx, types.NamespacedName{Name: lvdl.Name}, pv); err != nil {
		return lvdl, nil, fmt.Errorf("error getting associated pv object %s: %v", lvdl.Name, err)
	}
	return lvdl, pv, nil
}

func NewLocalVolumeDeviceLinkCache(client client.Client, mgr manager.Manager) *LocalVolumeDeviceLinkCache {
	return &LocalVolumeDeviceLinkCache{
		client:           client,
		mgr:              mgr,
		synced:           make(chan struct{}),
		localDeviceInfos: map[string]CurrentBlockDeviceInfo{},
	}
}

// IsSynced returns true once the LVDL informer has synced and event
// handlers have been registered. Reconcilers should skip cache-dependent
// work until this returns true.
func (l *LocalVolumeDeviceLinkCache) IsSynced() bool {
	select {
	case <-l.synced:
		return true
	default:
		return false
	}
}

// Start implements manager.Runnable. The manager calls this after its own
// caches are started, but we still need to wait for the LVDL informer
// (which may have been dynamically added) to complete its initial sync.
func (l *LocalVolumeDeviceLinkCache) Start(ctx context.Context) error {
	informer, err := l.mgr.GetCache().GetInformer(ctx, &v1.LocalVolumeDeviceLink{})
	if err != nil {
		return err
	}

	_, err = l.watchForEvents(informer)
	if err != nil {
		return err
	}
	// Wait for the LVDL informer to complete its initial list.
	if !l.mgr.GetCache().WaitForCacheSync(ctx) {
		return fmt.Errorf("cache sync failed for LocalVolumeDeviceLink")
	}

	// Populate the cache from all existing LVDL objects before signalling
	// readiness, so reconcilers see a fully populated map.
	lvdlList := &v1.LocalVolumeDeviceLinkList{}
	if err := l.client.List(ctx, lvdlList); err != nil {
		return fmt.Errorf("failed to list LocalVolumeDeviceLink objects: %w", err)
	}
	for i := range lvdlList.Items {
		l.addOrUpdateLVDL(&lvdlList.Items[i])
	}

	// Signal that the cache is ready for use by reconcilers.
	close(l.synced)
	klog.InfoS("LVDL cache synced")

	<-ctx.Done()
	return nil
}

func (l *LocalVolumeDeviceLinkCache) watchForEvents(informer cache.Informer) (k8scache.ResourceEventHandlerRegistration, error) {
	// Watch for subsequent changes.
	return informer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if lvdl, ok := obj.(*v1.LocalVolumeDeviceLink); ok {
				l.addOrUpdateLVDL(lvdl)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			if lvdl, ok := newObj.(*v1.LocalVolumeDeviceLink); ok {
				l.addOrUpdateLVDL(lvdl)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(k8scache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if lvdl, ok := obj.(*v1.LocalVolumeDeviceLink); ok {
				l.removeLVDL(lvdl)
			}
		},
	})
}

func (l *LocalVolumeDeviceLinkCache) FindStalePVs(symlink string, blockDevice internal.BlockDevice) (CurrentBlockDeviceInfo, bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	info, ok := l.localDeviceInfos[symlink]
	if ok {
		cloned := CurrentBlockDeviceInfo{lvdls: make(map[string]*v1.LocalVolumeDeviceLink, len(info.lvdls))}
		maps.Copy(cloned.lvdls, info.lvdls)
		return cloned, true, nil
	}

	// we couldn't find a direct match using symlink, lets try to find LVDL using sibling symlinks
	// that belong to same device.
	validLinkTargets, err := blockDevice.GetValidByIDSymlinks()
	if err != nil {
		return info, false, fmt.Errorf("error listing valid symlinks in %s for finding stale pvs for device %s: %w", internal.DiskByIDDir, blockDevice.Name, err)
	}

	currentDeviceInfo := CurrentBlockDeviceInfo{
		lvdls: map[string]*v1.LocalVolumeDeviceLink{},
	}

	for _, linkTarget := range validLinkTargets {
		deviceInfo, ok := l.localDeviceInfos[linkTarget]
		if ok {
			maps.Copy(currentDeviceInfo.lvdls, deviceInfo.lvdls)
		}
	}
	if len(currentDeviceInfo.lvdls) == 0 {
		return currentDeviceInfo, false, nil
	}
	return currentDeviceInfo, true, nil
}

// AddOrUpdateLVDL updates the in-memory index immediately, enabling
// write-through cache semantics when called after a successful API server write.
func (l *LocalVolumeDeviceLinkCache) AddOrUpdateLVDL(lvdl *v1.LocalVolumeDeviceLink) {
	l.addOrUpdateLVDL(lvdl)
}

// SeedForTests inserts an LVDL entry into the in-memory map.
// This is used by unit tests that don't run the informer-backed Start() flow.
func (l *LocalVolumeDeviceLinkCache) SeedForTests(lvdl *v1.LocalVolumeDeviceLink) {
	l.addOrUpdateLVDL(lvdl)
}

// MarkSyncedForTests marks the cache as ready without starting informers.
func (l *LocalVolumeDeviceLinkCache) MarkSyncedForTests() {
	select {
	case <-l.synced:
		return
	default:
		close(l.synced)
	}
}

func (l *LocalVolumeDeviceLinkCache) addOrUpdateLVDL(lvdl *v1.LocalVolumeDeviceLink) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Build a set of the new valid targets for fast lookup.
	newTargets := make(map[string]struct{}, len(lvdl.Status.ValidLinkTargets))
	for _, t := range lvdl.Status.ValidLinkTargets {
		newTargets[t] = struct{}{}
	}

	// Remove this LVDL from any existing entries whose keys are no longer
	// in the new ValidLinkTargets (handles target changes across updates).
	for symlink, deviceInfo := range l.localDeviceInfos {
		if _, still := newTargets[symlink]; still {
			continue
		}
		if _, has := deviceInfo.lvdls[lvdl.Name]; !has {
			continue
		}
		delete(deviceInfo.lvdls, lvdl.Name)
		if len(deviceInfo.lvdls) == 0 {
			delete(l.localDeviceInfos, symlink)
		} else {
			l.localDeviceInfos[symlink] = deviceInfo
		}
	}

	// Add/update the LVDL for each current target.
	for _, linkTarget := range lvdl.Status.ValidLinkTargets {
		deviceInfo, ok := l.localDeviceInfos[linkTarget]
		if !ok {
			deviceInfo = CurrentBlockDeviceInfo{
				lvdls: map[string]*v1.LocalVolumeDeviceLink{},
			}
		}
		deviceInfo.lvdls[lvdl.Name] = lvdl
		l.localDeviceInfos[linkTarget] = deviceInfo
	}
}

func (l *LocalVolumeDeviceLinkCache) removeLVDL(lvdl *v1.LocalVolumeDeviceLink) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, linkTarget := range lvdl.Status.ValidLinkTargets {
		deviceInfo, ok := l.localDeviceInfos[linkTarget]
		if !ok {
			continue
		}
		delete(deviceInfo.lvdls, lvdl.Name)
		if len(deviceInfo.lvdls) == 0 {
			delete(l.localDeviceInfos, linkTarget)
		} else {
			l.localDeviceInfos[linkTarget] = deviceInfo
		}
	}
}
