package internal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeviceLinkHandler struct {
	currentSymlink   string
	lvdlName         string
	preferredSymlink string

	pvName    string
	namespace string

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

func (dl *DeviceLinkHandler) Create(ctx context.Context, pvName, namespace string) (*v1.LocalVolumeDeviceLink, error) {
	dl.lvdlName = pvName
	dl.pvName = pvName
	dl.namespace = namespace

	deviceLink := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dl.lvdlName,
			Namespace: namespace,
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: dl.lvdlName,
			Policy:               v1.DeviceLinkPolicyNone,
		},
	}

	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: dl.lvdlName, Namespace: namespace}
	err := dl.client.Get(ctx, key, existing)

	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting localvolumedevicelink object: %w", err)
	}

	if err != nil && apierrors.IsNotFound(err) {
		if err = dl.client.Create(ctx, deviceLink); err != nil {
			return nil, err
		}
		dl.deviceLink = deviceLink
		return deviceLink, nil
	}

	// users are allowed to set different device policies
	existing.Spec.Policy = v1.DeviceLinkPolicyNone

	// if existing and deviceLink are same we can return
	if equality.Semantic.DeepEqual(existing, deviceLink) {
		return existing, nil
	}

	klog.Infof("updating lvdl object: %s", dl.lvdlName)

	existingCopy := existing.DeepCopy()
	existingCopy.Spec.PersistentVolumeName = deviceLink.Spec.PersistentVolumeName

	err = dl.client.Update(ctx, existingCopy)
	if err != nil {
		return nil, fmt.Errorf("error updating localvolumedevicelink object: %w", err)
	}
	dl.deviceLink = existingCopy
	return existingCopy, nil
}

func (dl *DeviceLinkHandler) UpdateStatusAndPV(ctx context.Context, kName, devicePath string) (*v1.LocalVolumeDeviceLink, error) {
	klog.Infof("updating lvdl with currentSymlink: %s, preferredSymlink: %s, devicePath: %s, kname: %s", dl.currentSymlink, dl.preferredSymlink, devicePath, kName)
	validLinks, err := dl.getValidByIDSymlinks(kName)
	if err != nil {
		return nil, err
	}

	filesystemUUID, err := getFilesystemUUID(devicePath)
	if err != nil {
		return nil, err
	}

	existing := &v1.LocalVolumeDeviceLink{}
	key := types.NamespacedName{Name: dl.lvdlName, Namespace: dl.namespace}
	err = dl.client.Get(ctx, key, existing)
	if err != nil {
		return dl.deviceLink, err
	}

	updatedCopy := existing.DeepCopy()

	updatedCopy.Status.CurrentLinkTarget = dl.currentSymlink
	updatedCopy.Status.PreferredLinkTarget = dl.preferredSymlink
	updatedCopy.Status.ValidLinkTargets = validLinks
	updatedCopy.Status.FilesystemUUID = filesystemUUID

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
