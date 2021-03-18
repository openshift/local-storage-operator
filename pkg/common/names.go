package common

import (
	"fmt"
	"os"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	defaultDiskMakerImageVersion = "quay.io/openshift/origin-local-storage-diskmaker"
	defaultProvisionImage        = "quay.io/openshift/origin-local-storage-static-provisioner"
	defaultlocalDiskLocation     = "/mnt/local-storage"

	// ProvisionerServiceAccount is used by the diskmaker daemons
	ProvisionerServiceAccount = "local-storage-admin"

	// OwnerNamespaceLabel references the owning object's namespace
	OwnerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	// OwnerNameLabel references the owning object
	OwnerNameLabel = "local.storage.openshift.io/owner-name"

	// DiskMakerImageEnv is used by the operator to read the DISKMAKER_IMAGE from the environment
	DiskMakerImageEnv = "DISKMAKER_IMAGE"
	// ProvisionerImageEnv is used by the operator to read the PROVISIONER_IMAGE from the environment
	ProvisionerImageEnv = "PROVISIONER_IMAGE"
	// LocalDiskLocationEnv is passed to the operator to override the LOCAL_DISK_LOCATION host directory
	LocalDiskLocationEnv = "LOCAL_DISK_LOCATION"

	// ProvisionerConfigMapName is the name of the local-static-provisioner configmap
	ProvisionerConfigMapName = "local-provisioner"
)

// GetLocalProvisionerImage return the image to be used for provisioner daemonset
func GetLocalProvisionerImage() string {
	if provisionerImageFromEnv := os.Getenv(ProvisionerImageEnv); provisionerImageFromEnv != "" {
		return provisionerImageFromEnv
	}
	return defaultProvisionImage
}

// GetDiskMakerImage returns the image to be used for diskmaker daemonset
func GetDiskMakerImage() string {
	if diskMakerImageFromEnv := os.Getenv(DiskMakerImageEnv); diskMakerImageFromEnv != "" {
		return diskMakerImageFromEnv
	}
	return defaultDiskMakerImageVersion
}

// GetLocalDiskLocationPath return the local disk path
func GetLocalDiskLocationPath() string {
	if localDiskLocationEnvImage := os.Getenv(LocalDiskLocationEnv); localDiskLocationEnvImage != "" {
		return localDiskLocationEnvImage
	}
	return defaultlocalDiskLocation
}

// LocalVolumeKey returns key for the localvolume
func LocalVolumeKey(lv *localv1.LocalVolume) string {
	return fmt.Sprintf("%s/%s", lv.Namespace, lv.Name)
}

// GetProvisionedByValue is the the annotation that indicates which node a PV was originally provisioned on
// the key is provCommon.AnnProvisionedBy ("pv.kubernetes.io/provisioned-by")
func GetProvisionedByValue(node corev1.Node) string {
	return fmt.Sprintf("local-volume-provisioner-%v-%v", node.Name, node.UID)
}
