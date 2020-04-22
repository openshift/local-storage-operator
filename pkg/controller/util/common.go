package util

import "os"

const (
	defaultDiskMakerImageVersion = "quay.io/openshift/origin-local-storage-diskmaker"
	defaultProvisionImage        = "quay.io/openshift/origin-local-storage-static-provisioner"
	localDiskLocation            = "/mnt/local"

	ProvisionerServiceAccount    = "local-storage-admin"
	ProvisionerPVRoleBindingName = "local-storage-provisioner-pv-binding"
	ProvisionerNodeRoleName      = "local-storage-provisioner-node-clusterrole"

	LocalVolumeSetRoleName        = "local-storage-provisioner-cr-role"
	LocalVolumeSetRoleBindingName = "local-storage-provisioner-cr-rolebinding"

	DefaultPVClusterRole           = "system:persistent-volume-provisioner"
	ProvisionerNodeRoleBindingName = "local-storage-provisioner-node-binding"

	DiskMakerImageEnv    = "DISKMAKER_IMAGE"
	ProvisionerImageEnv  = "PROVISIONER_IMAGE"
	LocalDiskLocationEnv = "LOCAL_DISK_LOCATION"
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
	return localDiskLocation
}
