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
	"k8s.io/client-go/tools/record"
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
	client       client.Client
	clientReader client.Reader
	recorder     record.EventRecorder
}

func NewDeviceLinkHandler(client client.Client, clientReader client.Reader, recorder record.EventRecorder) *DeviceLinkHandler {
	return &DeviceLinkHandler{
		client:       client,
		clientReader: clientReader,
		recorder:     recorder,
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

func (dl *DeviceLinkHandler) ApplyStatus(ctx context.Context, pvName, namespace string, blockDevice BlockDevice, ownerObj runtime.Object, currentSymlink string) (*v1.LocalVolumeDeviceLink, error) {
	devicePath, err := blockDevice.GetDevPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get /dev path for %s: %w", blockDevice.Name, err)
	}

	// Update is best-effort and independent from Create: if the PV
	// does not exist yet, return without doing anything.
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

	copyToUpdate := existing.DeepCopy()
	copyToUpdate, err = dl.setStatusSymlinks(copyToUpdate, blockDevice, "", currentSymlink)
	if err != nil {
		klog.ErrorS(err, "error setting status symlinks")
		return existing, err
	}

	if equality.Semantic.DeepEqual(existing.Status, copyToUpdate.Status) {
		klog.V(4).Infof("updating lvdl %s status is not required", pvName)
		return existing, nil
	}

	if existing.Status.PreferredLinkTarget != copyToUpdate.Status.PreferredLinkTarget {
		infoUpdate := fmt.Sprintf("PreferredLinkTarget has changed from %s to %s for device %s", existing.Status.PreferredLinkTarget, copyToUpdate.Status.PreferredLinkTarget, blockDevice.Name)
		klog.Info(infoUpdate)
		ownerObj, _ := dl.resolveOwnerObjectFromLVDL(ctx, copyToUpdate)
		if ownerObj != nil {
			dl.recorder.Eventf(ownerObj, corev1.EventTypeNormal, "PreferredSymlinkChanged", infoUpdate)
		}
	}

	err = dl.client.Status().Update(ctx, copyToUpdate)

	return copyToUpdate, err
}

func (dl *DeviceLinkHandler) setStatusSymlinks(lvdl *v1.LocalVolumeDeviceLink, blockDevice BlockDevice, preferredLinkTarget, currentSymlink string) (*v1.LocalVolumeDeviceLink, error) {
	devicePath, err := blockDevice.GetDevPath()
	if err != nil {
		return lvdl, fmt.Errorf("failed to get /dev path for %s: %w", blockDevice.Name, err)
	}

	if preferredLinkTarget == "" {
		preferredLinkTarget, err = blockDevice.GetUncachedPathID()
		if err != nil {
			// IDPathNotFoundError means no by-id symlink exists for this device;
			// treat it as "no preferred symlink" rather than a hard error.
			var idNotFound IDPathNotFoundError
			if !errors.As(err, &idNotFound) {
				return lvdl, fmt.Errorf("failed to get preferred device link for %s: %w", blockDevice.Name, err)
			}
			preferredLinkTarget = ""
		}
	}
	klog.V(2).Infof("updating lvdl with currentSymlink: %s, preferredSymlink: %s, devicePath: %s, kname: %s", currentSymlink, preferredLinkTarget, devicePath, blockDevice.KName)

	validLinks, err := dl.getValidByIDSymlinks(blockDevice.KName)
	if err != nil {
		return lvdl, err
	}

	filesystemUUID, err := getFilesystemUUID(devicePath)
	if err != nil {
		return lvdl, err
	}
	klog.V(4).Infof("updating lvdl %s with, filesystemUUID: %s, validLinks: %+v", lvdl.Name, filesystemUUID, validLinks)

	lvdl.Status.CurrentLinkTarget = currentSymlink
	lvdl.Status.PreferredLinkTarget = preferredLinkTarget
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
		return nil, fmt.Errorf("unable to determine owner for LocalVolumeDeviceLink %s", pvName)
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
// TODO: Compute PreferredLinkTarget dynamically by reading the filesystem
func (dl *DeviceLinkHandler) RecreateSymlinkIfNeeded(ctx context.Context, lvdl *v1.LocalVolumeDeviceLink, symLinkPath string, blockDevice BlockDevice) (*v1.LocalVolumeDeviceLink, error) {
	currentTarget := lvdl.Status.CurrentLinkTarget
	preferredTarget, err := blockDevice.GetPathByID()
	if err != nil {
		msg := fmt.Sprintf("couldn't find preferredLinkTarget for device %s with currentLink %s: %v", blockDevice.Name, currentTarget, err)
		condition := getCondition("PreferredTargetNotFound", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice, "", currentTarget)
	}

	klog.InfoS("RecreateSymlinkIfNeeded: symlink needs update",
		"pvName", lvdl.Name, "currentTarget", currentTarget, "preferredTarget", preferredTarget)

	// 8. Validate device identity: preferredTarget and currentTarget must resolve to the same device
	resolvedPreferred, err := FilePathEvalSymLinks(preferredTarget)
	if err != nil {
		msg := fmt.Sprintf("failed to eval preferred target %s: %v", preferredTarget, err)
		condition := getCondition("EvalSymlinkFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice, preferredTarget, currentTarget)
	}

	symLinkDir := filepath.Dir(symLinkPath)
	entries, err := FilePathGlob(symLinkDir + "/*")
	if err != nil {
		msg := fmt.Sprintf("failed to list symlink dir %s: %v", symLinkDir, err)
		condition := getCondition("ListDirFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice, preferredTarget, currentTarget)
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
			return dl.updateStatus(ctx, lvdl, condition, blockDevice, preferredTarget, currentTarget)
		}
	}

	// 10. Perform atomic swap: create temp symlink then rename over the existing one
	tmpPath := symLinkPath + ".tmp"
	_ = os.Remove(tmpPath) // clean up any stale .tmp from a previous failed attempt
	defer func() { os.Remove(tmpPath) }()

	if err := os.Symlink(preferredTarget, tmpPath); err != nil {
		msg := fmt.Sprintf("failed to create temp symlink %s -> %s: %v", tmpPath, preferredTarget, err)
		condition := getCondition("TempSymlinkCreateFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice, preferredTarget, currentTarget)
	}

	if err := os.Rename(tmpPath, symLinkPath); err != nil {
		msg := fmt.Sprintf("failed to atomically replace symlink %s: %v", symLinkPath, err)
		condition := getCondition("RenameSymlinkFailed", msg, operatorv1.ConditionTrue)
		return dl.updateStatus(ctx, lvdl, condition, blockDevice, preferredTarget, currentTarget)
	}

	klog.InfoS("RecreateSymlinkIfNeeded: successfully updated symlink",
		"pvName", lvdl.Name, "symLinkPath", symLinkPath,
		"from", currentTarget, "to", preferredTarget)

	copyToUpdate := lvdl.DeepCopy()

	// After successful swap, the current symlink is now the preferred target
	copyToUpdate, err = dl.setStatusSymlinks(copyToUpdate, blockDevice, preferredTarget, preferredTarget)
	if err != nil {
		klog.ErrorS(err, "error refreshing lvdl status after symlink recreation", "lvdl", lvdl.Name)
		return lvdl, fmt.Errorf("refreshing lvdl status after symlink recreation failed: %w", err)
	}
	// clear out any error conditions we previously reported.
	copyToUpdate.Status.Conditions = []operatorv1.OperatorCondition{}

	err = dl.client.Status().Update(ctx, copyToUpdate)
	if err != nil {
		klog.ErrorS(err, "error updating lvdl object", "lvdl", lvdl.Name)
		return lvdl, fmt.Errorf("updating lvdl failed with: %w", err)
	}
	ownerObj, ownerErr := dl.resolveOwnerObjectFromLVDL(ctx, lvdl)
	if ownerErr != nil {
		klog.ErrorS(ownerErr, "unable to resolve owner object for symlink recreated event", "lvdl", lvdl.Name)
	} else {
		dl.recorder.Eventf(ownerObj, corev1.EventTypeNormal, "SymlinkRecreated",
			"Successfully updated symlink %s from %s to %s", symLinkPath, currentTarget, preferredTarget)
	}

	return copyToUpdate, err
}

func (dl *DeviceLinkHandler) resolveOwnerObjectFromLVDL(ctx context.Context, lvdl *v1.LocalVolumeDeviceLink) (runtime.Object, error) {
	if lvdl == nil {
		return nil, fmt.Errorf("lvdl is nil")
	}
	ownerRef := metav1.GetControllerOf(lvdl)
	if ownerRef == nil {
		if len(lvdl.OwnerReferences) == 0 {
			return nil, fmt.Errorf("no owner references for lvdl %s", lvdl.Name)
		}
		ownerRef = &lvdl.OwnerReferences[0]
	}

	nn := types.NamespacedName{Name: ownerRef.Name, Namespace: lvdl.Namespace}
	switch ownerRef.Kind {
	case v1.LocalVolumeKind:
		owner := &v1.LocalVolume{}
		if err := dl.clientReader.Get(ctx, nn, owner); err != nil {
			return nil, err
		}
		return owner, nil
	case v1alpha1.LocalVolumeSetKind:
		owner := &v1alpha1.LocalVolumeSet{}
		if err := dl.clientReader.Get(ctx, nn, owner); err != nil {
			return nil, err
		}
		return owner, nil
	default:
		return nil, fmt.Errorf("unsupported owner kind %q for lvdl %s", ownerRef.Kind, lvdl.Name)
	}
}

func (dl *DeviceLinkHandler) updateStatus(ctx context.Context, lvdl *v1.LocalVolumeDeviceLink, condition operatorv1.OperatorCondition, blockDevice BlockDevice, preferredLinkTarget, currentSymlink string) (*v1.LocalVolumeDeviceLink, error) {
	copyToUpdate := lvdl.DeepCopy()
	copyToUpdate, err := dl.setStatusSymlinks(copyToUpdate, blockDevice, preferredLinkTarget, currentSymlink)
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

func HasMismatchingSymlink(lvdl *v1.LocalVolumeDeviceLink, blockDevice BlockDevice) bool {
	lvdlName := "<nil>"
	if lvdl != nil {
		lvdlName = lvdl.Name
	}
	klog.V(4).Infof("checking for mismatching symlinks, lvdl %s", lvdlName)
	if lvdl == nil {
		return false
	}
	if lvdl.Spec.Policy != v1.DeviceLinkPolicyPreferredLinkTarget {
		return false
	}

	preferredTarget, err := blockDevice.GetPathByID()
	if err != nil {
		klog.ErrorS(err, "error getting pathbyid for device", "device", blockDevice.Name)
		return false
	}

	currentTarget := lvdl.Status.CurrentLinkTarget
	klog.Infof("checking for mismatching symlinks current: %s, preferred: %s", currentTarget, preferredTarget)

	if preferredTarget == "" {
		return false
	}

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
