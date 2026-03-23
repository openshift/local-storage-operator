package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	v1 "github.com/openshift/local-storage-operator/api/v1"
	v1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	utilexec "k8s.io/utils/exec"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// timeNow is a mockable wrapper around metav1.Now for testing.
var timeNow = metav1.Now

const (
	// DeviceLinkSymlinkRecreateConditionType is the condition type set on LVDL when symlink recreation fails.
	DeviceSymlinkErrorType = "SymlinkRecreateError"
)

type DeviceLinkHandler struct {
	currentSymlink string
	client         client.Client
	clientReader   client.Reader
}

func NewDeviceLinkHandler(currentSymlink string, client client.Client, clientReader client.Reader) *DeviceLinkHandler {
	return &DeviceLinkHandler{
		currentSymlink: currentSymlink,
		client:         client,
		clientReader:   clientReader,
	}
}

func (dl *DeviceLinkHandler) createLVDL(ctx context.Context, pvName, namespace string, ownerObj runtime.Object) (*v1.LocalVolumeDeviceLink, error) {
	requiredLocalDeviceLink, err := dl.generateLVDLObj(pvName, namespace, ownerObj)
	if err != nil {
		return nil, err
	}

	if err = dl.client.Create(ctx, requiredLocalDeviceLink); err != nil {
		return nil, err
	}
	return requiredLocalDeviceLink, nil
}

func (dl *DeviceLinkHandler) generateLVDLObj(pvName, namespace string, ownerObj runtime.Object) (*v1.LocalVolumeDeviceLink, error) {
	ownerRefs, err := buildOwnerRefs(ownerObj)
	if err != nil {
		return nil, err
	}

	requiredLocalDeviceLink := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:            pvName,
			Namespace:       namespace,
			OwnerReferences: ownerRefs,
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: pvName,
			Policy:               v1.DeviceLinkPolicyNone,
		},
	}
	return requiredLocalDeviceLink, nil
}

func buildOwnerRefs(ownerObj runtime.Object) ([]metav1.OwnerReference, error) {
	if isNilOwnerObject(ownerObj) {
		return nil, fmt.Errorf("owner object is nil")
	}
	accessor, err := meta.Accessor(ownerObj)
	if err != nil {
		return nil, fmt.Errorf("could not get owner metadata accessor: %w", err)
	}
	kind := ownerObj.GetObjectKind().GroupVersionKind().Kind
	apiVersion := ownerObj.GetObjectKind().GroupVersionKind().GroupVersion().String()

	if kind == "" || apiVersion == "" {
		switch ownerObj.(type) {
		case *v1.LocalVolume:
			kind = v1.LocalVolumeKind
			apiVersion = v1.GroupVersion.String()
		case *v1alpha1.LocalVolumeSet:
			kind = v1.LocalVolumeSetKind
			apiVersion = v1alpha1.GroupVersion.String()
		default:
			return nil, fmt.Errorf("unsupported owner object type")
		}
	}
	trueVal := true
	return []metav1.OwnerReference{
		{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       accessor.GetName(),
			UID:        accessor.GetUID(),
			Controller: &trueVal,
		},
	}, nil
}

func isNilOwnerObject(ownerObj runtime.Object) bool {
	if ownerObj == nil {
		return true
	}
	value := reflect.ValueOf(ownerObj)
	return value.Kind() == reflect.Ptr && value.IsNil()
}

func (dl *DeviceLinkHandler) ApplyStatus(ctx context.Context, pvName, namespace string, blockDevice BlockDevice, ownerObj runtime.Object) (*v1.LocalVolumeDeviceLink, error) {
	devicePath, err := blockDevice.GetDevPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get /dev path for %s: %w", blockDevice.Name, err)
	}

	// Update is best-effort and independent from Create: if either the PV or
	// the LVDL does not exist yet, return without doing anything.
	existingPV := &corev1.PersistentVolume{}
	if err := dl.clientReader.Get(ctx, types.NamespacedName{Name: pvName}, existingPV); err != nil {
		if apierrors.IsNotFound(err) {
			klog.InfoS("skipping creation of lvdl object, no pv exists", "devicePath", devicePath)
			return nil, nil
		}
		return nil, err
	}

	existing, err := dl.findOrCreateLVDL(ctx, pvName, namespace, devicePath, ownerObj)
	if err != nil {
		return nil, err
	}

	// rare case when ownerObj is nil
	if existing == nil {
		return nil, nil
	}
	copyToUpdate := existing.DeepCopy()
	copyToUpdate, err = dl.setStatusSymlinks(copyToUpdate, blockDevice)
	if err != nil {
		klog.ErrorS(err, "error setting status symlinks")
		return existing, err
	}

	if equality.Semantic.DeepEqual(existing.Status, copyToUpdate.Status) {
		klog.V(4).Infof("updating lvdl %s status is not required", pvName)
		return existing, nil
	}

	err = dl.client.Status().Update(ctx, copyToUpdate)

	return copyToUpdate, err
}

func (dl *DeviceLinkHandler) setStatusSymlinks(lvdl *v1.LocalVolumeDeviceLink, blockDevice BlockDevice) (*v1.LocalVolumeDeviceLink, error) {
	devicePath, err := blockDevice.GetDevPath()
	if err != nil {
		return lvdl, fmt.Errorf("failed to get /dev path for %s: %w", blockDevice.Name, err)
	}

	preferredSymlink, err := blockDevice.GetUncachedPathID()
	if err != nil {
		// IDPathNotFoundError means no by-id symlink exists for this device;
		// treat it as "no preferred symlink" rather than a hard error.
		var idNotFound IDPathNotFoundError
		if !errors.As(err, &idNotFound) {
			return lvdl, fmt.Errorf("failed to get preferred device link for %s: %w", blockDevice.Name, err)
		}
		preferredSymlink = ""
	}
	klog.V(2).Infof("updating lvdl with currentSymlink: %s, preferredSymlink: %s, devicePath: %s, kname: %s", dl.currentSymlink, preferredSymlink, devicePath, blockDevice.KName)

	validLinks, err := dl.getValidByIDSymlinks(blockDevice.KName)
	if err != nil {
		return lvdl, err
	}

	filesystemUUID, err := getFilesystemUUID(devicePath)
	if err != nil {
		return lvdl, err
	}
	klog.V(4).Infof("updating lvdl %s with, filesystemUUID: %s, validLinks: %+v", lvdl.Name, filesystemUUID, validLinks)

	lvdl.Status.CurrentLinkTarget = dl.currentSymlink
	lvdl.Status.PreferredLinkTarget = preferredSymlink
	lvdl.Status.ValidLinkTargets = validLinks
	lvdl.Status.FilesystemUUID = filesystemUUID
	return lvdl, nil
}

func (dl *DeviceLinkHandler) FindLVDL(ctx context.Context, lvdlName, namespace string) (*v1.LocalVolumeDeviceLink, error) {
	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: lvdlName, Namespace: namespace}

	// read without caching
	err := dl.clientReader.Get(ctx, key, existing)
	if err == nil {
		return existing, nil
	}
	return nil, err
}

func (dl *DeviceLinkHandler) findOrCreateLVDL(ctx context.Context, pvName, namespace, devicePath string, ownerObj runtime.Object) (*v1.LocalVolumeDeviceLink, error) {
	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: pvName, Namespace: namespace}
	err := dl.clientReader.Get(ctx, key, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if isNilOwnerObject(ownerObj) {
		klog.Warningf("missing lvdl object %s during status update, but owner is nil; skipping creation for device: %s", pvName, devicePath)
		return nil, nil
	}
	klog.Warningf("missing lvdl object %s during status update, creating one now for device: %s", pvName, devicePath)
	existing, err = dl.createLVDL(ctx, pvName, namespace, ownerObj)
	if apierrors.IsAlreadyExists(err) {
		return dl.FindLVDL(ctx, pvName, namespace)
	}
	if err != nil {
		return nil, fmt.Errorf("error creating lvdl object %s, for device %s: %w", pvName, devicePath, err)
	}
	return existing, nil
}

func (dl *DeviceLinkHandler) getValidByIDSymlinks(kname string) ([]string, error) {
	paths, err := FilePathGlob(DiskByIDDir + "*")
	if err != nil {
		return nil, err
	}

	matches := sets.New[string]()

	for _, path := range paths {
		isMatch, err := PathEvalsToDiskLabel(path, kname)
		if err != nil {
			return nil, err
		}
		if isMatch {
			matches.Insert(path)
		}
	}

	return sets.List(matches), nil
}

func getFilesystemUUID(devicePath string) (string, error) {
	klog.InfoS("trying to get filesystem information", "devicePath", devicePath)
	cmd := CmdExecutor.Command("blkid", "-s", "UUID", "-o", "value", devicePath)
	output, err := executeCmdWithCombinedOutput(cmd)
	if err != nil {
		// blkid returns 2 when no UUID is found for the device.
		if exitErr, ok := err.(utilexec.ExitError); ok && exitErr.ExitStatus() == 2 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// RecreateSymlinkIfNeeded checks the LVDL policy and atomically recreates
// the symlink if policy is PreferredLinkTarget and currentLinkTarget != preferredLinkTarget.
// symLinkPath is the full path under /mnt/local-storage/<storageClass>/<deviceName>.
// Returns nil if no action is needed or action succeeded, error if action failed.
// On error, it sets a failure OperatorCondition on the LVDL object.
func (dl *DeviceLinkHandler) RecreateSymlinkIfNeeded(ctx context.Context, lvdl *v1.LocalVolumeDeviceLink, symLinkPath string, blockDevice BlockDevice) (*v1.LocalVolumeDeviceLink, error) {
	currentTarget := lvdl.Status.CurrentLinkTarget
	preferredTarget := lvdl.Status.PreferredLinkTarget

	klog.InfoS("RecreateSymlinkIfNeeded: symlink needs update",
		"pvName", lvdl.Name, "currentTarget", currentTarget, "preferredTarget", preferredTarget)

	// 7. Validate preferredTarget exists on disk and is a symlink
	preferredInfo, err := os.Lstat(preferredTarget)
	if err != nil {
		msg := fmt.Sprintf("preferred target %s does not exist: %v", preferredTarget, err)
		condition := getCondition("PreferredTargetNotFound", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}
	if preferredInfo.Mode()&os.ModeSymlink == 0 {
		msg := fmt.Sprintf("preferred target %s is not a symlink", preferredTarget)
		condition := getCondition("PreferredTargetNotSymlink", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}

	// 8. Validate device identity: preferredTarget and currentTarget must resolve to the same device
	resolvedPreferred, err := FilePathEvalSymLinks(preferredTarget)
	if err != nil {
		msg := fmt.Sprintf("failed to eval preferred target %s: %v", preferredTarget, err)
		condition := getCondition("EvalSymlinkFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}

	resolvedCurrent, err := FilePathEvalSymLinks(currentTarget)
	if err == nil && resolvedCurrent != resolvedPreferred {
		msg := fmt.Sprintf("preferred target %s resolves to %s but current target %s resolves to %s: different devices",
			preferredTarget, resolvedPreferred, currentTarget, resolvedCurrent)
		condition := getCondition("DeviceMismatch", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}

	// if resolvedCurrent fails, currentTarget is gone — proceed with recreation anyway

	// 9. Validate no OTHER symlink in the directory already points to resolvedPreferred
	symLinkDir := filepath.Dir(symLinkPath)
	entries, err := FilePathGlob(symLinkDir + "/*")
	if err != nil {
		msg := fmt.Sprintf("failed to list symlink dir %s: %v", symLinkDir, err)
		condition := getCondition("ListDirFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}
	for _, entry := range entries {
		if entry == symLinkPath {
			continue // this is the symlink we are replacing
		}
		resolvedEntry, err := FilePathEvalSymLinks(entry)
		if err != nil {
			continue // broken symlink — skip
		}
		if resolvedEntry == resolvedPreferred {
			msg := fmt.Sprintf("preferred target %s is already claimed by symlink %s", preferredTarget, entry)
			condition := getCondition("TargetAlreadyClaimed", msg, operatorv1.ConditionTrue)
			return dl.updateStatus(ctx, lvdl, condition, blockDevice)
		}
	}

	// 10. Perform atomic swap: create temp symlink then rename over the existing one
	tmpPath := symLinkPath + ".tmp"
	_ = os.Remove(tmpPath) // clean up any stale .tmp from a previous failed attempt
	defer func() { os.Remove(tmpPath) }()

	if err := os.Symlink(preferredTarget, tmpPath); err != nil {
		msg := fmt.Sprintf("failed to create temp symlink %s -> %s: %v", tmpPath, preferredTarget, err)
		condition := getCondition("TempSymlinkCreateFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}

	if err := os.Rename(tmpPath, symLinkPath); err != nil {
		msg := fmt.Sprintf("failed to atomically replace symlink %s: %v", symLinkPath, err)
		condition := getCondition("RenameSymlinkFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice)
	}

	klog.InfoS("RecreateSymlinkIfNeeded: successfully updated symlink",
		"pvName", lvdl.Name, "symLinkPath", symLinkPath,
		"from", currentTarget, "to", preferredTarget)

	copyToUpdate := lvdl.DeepCopy()

	dl.currentSymlink = preferredTarget

	copyToUpdate, err = dl.setStatusSymlinks(copyToUpdate, blockDevice)
	if err != nil {
		klog.ErrorS(err, "error refreshing lvdl status after symlink recreation", "lvdl", lvdl.Name)
		return lvdl, fmt.Errorf("refreshing lvdl status after symlink recreation failed: %w", err)
	}
	copyToUpdate.Status.CurrentLinkTarget = preferredTarget
	copyToUpdate.Status.Conditions = []operatorv1.OperatorCondition{}

	err = dl.client.Status().Update(ctx, copyToUpdate)
	if err != nil {
		klog.ErrorS(err, "error updating lvdl object", "lvdl", lvdl.Name)
		return lvdl, fmt.Errorf("updating lvdl failed with: %w", err)
	}

	return copyToUpdate, err
}

func (dl *DeviceLinkHandler) updateStatus(ctx context.Context, lvdl *v1.LocalVolumeDeviceLink, condition operatorv1.OperatorCondition, blockDevice BlockDevice) (*v1.LocalVolumeDeviceLink, error) {
	copyToUpdate := lvdl.DeepCopy()
	copyToUpdate, err := dl.setStatusSymlinks(copyToUpdate, blockDevice)
	if err != nil {
		klog.ErrorS(err, "error setting status symlinks")
		return lvdl, fmt.Errorf("symlink recreation failed %s, setting conditions also failed with: %w", condition.Message, err)
	}

	copyToUpdate = dl.setLVDLCondition(copyToUpdate, condition)
	if equality.Semantic.DeepEqual(lvdl.Status, copyToUpdate.Status) {
		klog.V(4).Infof("updating lvdl %s status is not required", lvdl.Name)
		return lvdl, nil
	}

	err = dl.client.Status().Update(ctx, copyToUpdate)
	if err != nil {
		klog.ErrorS(err, "error updating lvdl object", "lvdl", lvdl.Name, "symlinkError", condition.Message)
		return lvdl, fmt.Errorf("symlink recreation failed %s, setting conditions also failed with: %w", condition.Message, err)
	}

	return copyToUpdate, err
}

// setLVDLCondition updates or adds a named condition on the LVDL status.
// status=ConditionTrue means the condition is active (e.g., an error).
// status=ConditionFalse means the condition is resolved.
func (dl *DeviceLinkHandler) setLVDLCondition(lvdl *v1.LocalVolumeDeviceLink, condition operatorv1.OperatorCondition) *v1.LocalVolumeDeviceLink {
	index := -1
	existingConditions := lvdl.Status.Conditions
	for i, icond := range existingConditions {
		if icond.Type == condition.Type {
			index = i
		}
	}

	if index != -1 {
		existingCondition := existingConditions[index]
		if existingCondition.Message == condition.Message &&
			existingCondition.Reason == condition.Reason &&
			existingCondition.Status == condition.Status {
			return lvdl
		}
		existingConditions[index] = condition
	} else {
		existingConditions = append(existingConditions, condition)
	}
	lvdl.Status.Conditions = existingConditions
	return lvdl
}

func HasMismatchingSymlink(lvdl *v1.LocalVolumeDeviceLink) bool {
	if lvdl == nil {
		return false
	}
	if lvdl.Spec.Policy != v1.DeviceLinkPolicyPreferredLinkTarget {
		return false
	}

	preferredTarget := lvdl.Status.PreferredLinkTarget
	currentTarget := lvdl.Status.CurrentLinkTarget

	// 5. No preferred target known yet — wait for ApplyStatus to populate it
	if preferredTarget == "" {
		return false
	}

	// 6. Already correct — clear any stale failure condition and return
	if currentTarget == preferredTarget {
		return false
	}
	return true
}

func getCondition(reason, msg string, status operatorv1.ConditionStatus) operatorv1.OperatorCondition {
	now := timeNow()
	return operatorv1.OperatorCondition{
		Type:               DeviceSymlinkErrorType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
	}
}
