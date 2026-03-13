package internal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeviceLinkHandler struct {
	currentSymlink   string
	lvdlName         string
	preferredSymlink string

	deviceLink *v1.LocalVolumeDeviceLink

	// devicePoints to current device such as /dev/sda or something similar
	// so basically this is direct path in /dev filesystem to which
	// currentSymlink or preferredSymlink resolves to
	devicePath string
	client     client.Client
}

func NewDeviceLinkHandler(currentSymlink, preferredSymlink string, client client.Client) *DeviceLinkHandler {
	return &DeviceLinkHandler{
		currentSymlink:   currentSymlink,
		preferredSymlink: preferredSymlink,
		client:           client,
	}
}

func (dl *DeviceLinkHandler) Create(ctx context.Context, pvName, namespace string, ownerObj runtime.Object) (*v1.LocalVolumeDeviceLink, error) {
	dl.lvdlName = pvName

	ownerRefs, err := buildOwnerRefs(ownerObj)
	if err != nil {
		return nil, err
	}

	requiredLocalDeviceLink := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:            dl.lvdlName,
			Namespace:       namespace,
			OwnerReferences: ownerRefs,
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: dl.lvdlName,
			Policy:               v1.DeviceLinkPolicyNone,
		},
	}

	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: dl.lvdlName, Namespace: namespace}
	err = dl.client.Get(ctx, key, existing)

	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting localvolumedevicelink object: %w", err)
	}

	if err != nil && apierrors.IsNotFound(err) {
		if err = dl.client.Create(ctx, requiredLocalDeviceLink); err != nil {
			return nil, err
		}
		dl.deviceLink = requiredLocalDeviceLink
		return requiredLocalDeviceLink, nil
	}

	// Keep user-configured policy and only reconcile the pv name.
	if existing.Spec.PersistentVolumeName == requiredLocalDeviceLink.Spec.PersistentVolumeName {
		return existing, nil
	}

	klog.Infof("updating lvdl object: %s", dl.lvdlName)

	existingCopy := existing.DeepCopy()
	existingCopy.Spec.PersistentVolumeName = requiredLocalDeviceLink.Spec.PersistentVolumeName

	err = dl.client.Update(ctx, existingCopy)
	if err != nil {
		return nil, fmt.Errorf("error updating localvolumedevicelink object: %w", err)
	}
	dl.deviceLink = existingCopy
	return existingCopy, nil
}

func buildOwnerRefs(ownerObj runtime.Object) ([]metav1.OwnerReference, error) {
	if ownerObj == nil {
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

func (dl *DeviceLinkHandler) UpdateStatus(ctx context.Context, pvName, namespace, kName, devicePath string) (*v1.LocalVolumeDeviceLink, error) {
	klog.Infof("updating lvdl with currentSymlink: %s, preferredSymlink: %s, devicePath: %s, kname: %s", dl.currentSymlink, dl.preferredSymlink, devicePath, kName)

	// Update is best-effort and independent from Create: if either the PV or
	// the LVDL does not exist yet, return without doing anything.
	existingPV := &corev1.PersistentVolume{}
	if err := dl.client.Get(ctx, types.NamespacedName{Name: pvName}, existingPV); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: pvName, Namespace: namespace}
	err := dl.client.Get(ctx, key, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return dl.deviceLink, err
	}

	validLinks, err := dl.getValidByIDSymlinks(kName)
	if err != nil {
		return nil, err
	}

	filesystemUUID, err := getFilesystemUUID(devicePath)
	if err != nil {
		return nil, err
	}
	klog.Infof("updating lvdl %s with, filesystemUUID: %s, validLinks: %+v", pvName, filesystemUUID, validLinks)

	updatedCopy := existing.DeepCopy()

	updatedCopy.Status.CurrentLinkTarget = dl.currentSymlink
	updatedCopy.Status.PreferredLinkTarget = dl.preferredSymlink
	updatedCopy.Status.ValidLinkTargets = validLinks
	updatedCopy.Status.FilesystemUUID = filesystemUUID

	if equality.Semantic.DeepEqual(existing.Status, updatedCopy.Status) {
		klog.V(4).Infof("updating lvdl %s status is not required", pvName)
		return existing, nil
	}

	err = dl.client.Status().Update(ctx, updatedCopy)

	return updatedCopy, err
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
	klog.Infof("trying to get filesystem information for %s", devicePath)
	cmd := ExecCommand("blkid", "-s", "UUID", "-o", "value", devicePath)
	output, err := executeCmdWithCombinedOutput(cmd)
	if err != nil {
		// blkid returns 2 when no UUID is found for the device.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(output), nil
}
