package lvset

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

func (r *ReconcileLocalVolumeSet) createPV(
	obj *localv1alpha1.LocalVolumeSet,
	devLogger logr.Logger,
	storageClass storagev1.StorageClass,
	mountPointMap sets.String,
	symLinkPath string,
) error {
	useJob := false
	nodeLabels := r.runtimeConfig.Node.GetLabels()
	hostname, found := nodeLabels[corev1.LabelHostname]
	if !found {
		return fmt.Errorf("could node find label %q for node %q", corev1.LabelHostname, r.runtimeConfig.Node.GetName())
	}

	pvName := generatePVName(symLinkPath, r.runtimeConfig.Node.GetName(), storageClass.GetName())

	pvLogger := devLogger.WithValues("pv.Name", pvName)

	nodeAffinity := &corev1.VolumeNodeAffinity{
		Required: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      corev1.LabelHostname,
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{hostname},
						},
					},
				},
			},
		},
	}

	mountConfig, found := r.runtimeConfig.DiscoveryMap[storageClass.GetName()]
	if !found {
		return fmt.Errorf("could not find config for storageClass: %q", storageClass.GetName())
	}

	desiredVolumeMode := corev1.PersistentVolumeMode(mountConfig.VolumeMode)

	actualVolumeMode, err := provCommon.GetVolumeMode(r.runtimeConfig.VolUtil, symLinkPath)
	if err != nil {
		return fmt.Errorf("could not read the device's volume mode from the node: %w", err)
	}

	if r.cleanupTracker.InProgress(pvName, useJob) {
		pvLogger.Info("PV is still being cleaned, not going to recreate it")
		return nil
	}

	_, _, err = r.cleanupTracker.RemoveStatus(pvName, useJob)
	if err != nil {
		pvLogger.Error(err, "expected status exists and fail to remove cleanup status")
		return err
	}

	var capacityBytes int64
	switch actualVolumeMode {
	case corev1.PersistentVolumeBlock:
		capacityBytes, err = r.runtimeConfig.VolUtil.GetBlockCapacityByte(symLinkPath)
		if err != nil {
			return fmt.Errorf("could not read device capacity: %w", err)
		}
		if desiredVolumeMode == corev1.PersistentVolumeBlock && len(storageClass.MountOptions) != 0 {
			klog.Warningf("Path %q will be used to create block volume, "+
				"mount options %v will not take effect.", symLinkPath, storageClass.MountOptions)
		}
	case corev1.PersistentVolumeFilesystem:
		if desiredVolumeMode == corev1.PersistentVolumeBlock {
			return fmt.Errorf("path %q of filesystem mode cannot be used to create block volume", symLinkPath)
		}
		// Validate that this path is an actual mountpoint
		if !mountPointMap.Has(symLinkPath) {
			return fmt.Errorf("path %q is not an actual mountpoint", symLinkPath)
		}
		capacityBytes, err = r.runtimeConfig.VolUtil.GetFsCapacityByte(symLinkPath)
		if err != nil {
			return fmt.Errorf("path %q fs stats error: %w", symLinkPath, err)
		}
		// totalCapacityFSBytes += capacityByte
	default:
		return fmt.Errorf("path %q has unexpected volume type %q", symLinkPath, actualVolumeMode)
	}

	labels := map[string]string{
		corev1.LabelHostname:         hostname,
		common.PVOwnerKindLabel:      obj.Kind,
		common.PVOwnerNamespaceLabel: obj.GetNamespace(),
		common.PVOwnerNameLabel:      obj.GetName(),
	}
	var reclaimPolicy corev1.PersistentVolumeReclaimPolicy
	if storageClass.ReclaimPolicy == nil {
		devLogger.Error(fmt.Errorf("no ReclaimPolicy set in storageclass"), "defaulting to delete")
		reclaimPolicy = corev1.PersistentVolumeReclaimDelete
	} else {
		reclaimPolicy = *storageClass.ReclaimPolicy
	}

	localPVConfig := &provCommon.LocalPVConfig{
		Name:            pvName,
		HostPath:        symLinkPath,
		Capacity:        common.RoundDownCapacityPretty(capacityBytes), // d.VolUtil.GetBlockCapacityByte(filePath)
		StorageClass:    storageClass.GetName(),
		ReclaimPolicy:   reclaimPolicy,        // fetch from storageClass (created by LocalVolumeSet operator controller)
		ProvisionerName: r.runtimeConfig.Name, // populate in runtimeconfig earlier
		VolumeMode:      desiredVolumeMode,
		MountOptions:    storageClass.MountOptions, // d.getMountOptionsFromStorageClass(class)
		SetPVOwnerRef:   true,                      // set owner reference from node to PV
		OwnerReference: &metav1.OwnerReference{
			Kind:       r.runtimeConfig.Node.Kind,
			APIVersion: r.runtimeConfig.Node.APIVersion,
			Name:       r.runtimeConfig.Node.GetName(),
			UID:        r.runtimeConfig.Node.GetUID(),
		},
		NodeAffinity: nodeAffinity,
	}
	if desiredVolumeMode == corev1.PersistentVolumeFilesystem && obj.Spec.FSType != "" {
		localPVConfig.FsType = &obj.Spec.FSType
	}
	newPV := provCommon.CreateLocalPVSpec(localPVConfig)

	existingPV := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: pvName}}

	pvLogger.Info("creating")
	opRes, err := controllerutil.CreateOrUpdate(context.TODO(), r.client, existingPV, func() error {
		if existingPV.CreationTimestamp.IsZero() {
			// operations for create
			newPV.DeepCopyInto(existingPV)
		}
		// operations for update only

		// pv object says block, but the path is fs (the oppposite is fine)
		if existingPV.Spec.VolumeMode != nil &&
			*existingPV.Spec.VolumeMode == corev1.PersistentVolumeBlock && actualVolumeMode == corev1.PersistentVolumeFilesystem {
			err := fmt.Errorf("incorrect Volume Mode: PV requires block mode but path was in fs mode")
			pvLogger.Error(err, "pvName", pvName, "filePath", symLinkPath)
			r.runtimeConfig.Recorder.Eventf(existingPV, corev1.EventTypeWarning, provCommon.EventVolumeFailedDelete, err.Error())
		}

		// replace labels if and only if they don't already exist
		common.InitMapIfNil(&existingPV.ObjectMeta.Labels)
		for labelKey := range labels {
			_, found := existingPV.ObjectMeta.Labels[labelKey]
			if !found {
				existingPV.ObjectMeta.Labels[labelKey] = labels[labelKey]
			}
		}

		return nil
	})
	if opRes != controllerutil.OperationResultNone {
		pvLogger.Info("pv changed", "operation", opRes)
	}

	return err
}
