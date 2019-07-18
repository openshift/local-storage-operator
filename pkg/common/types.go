package common

import (
	"fmt"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// LocalVolumeOwnerNameForPV stores name of LocalVolume that created this PV
	LocalVolumeOwnerNameForPV = "storage.openshift.com/local-volume-owner-name"
	// LocalVolumeOwnerNamespaceForPV stores namespace of LocalVolume that created this PV
	LocalVolumeOwnerNamespaceForPV = "storage.openshift.com/local-volume-owner-namespace"
)

// GetPVOwnerSelector returns selector for selecting pvs owned by given volume
func GetPVOwnerSelector(lv *localv1.LocalVolume) labels.Selector {
	pvOwnerLabels := labels.Set{
		LocalVolumeOwnerNameForPV:      lv.Name,
		LocalVolumeOwnerNamespaceForPV: lv.Namespace,
	}
	return labels.SelectorFromSet(pvOwnerLabels)
}

// LocalVolumeKey returns key for the localvolume
func LocalVolumeKey(lv *localv1.LocalVolume) string {
	return fmt.Sprintf("%s/%s", lv.Namespace, lv.Name)
}
