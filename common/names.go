package common

import (
	"fmt"
	"os"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	defaultDiskMakerImageVersion = "quay.io/openshift/origin-local-storage-diskmaker"
	defaultKubeProxyImage        = "quay.io/openshift/origin-kube-rbac-proxy:latest"
	defaultlocalDiskLocation     = "/mnt/local-storage"

	// OwnerNamespaceLabel references the owning object's namespace
	OwnerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	// OwnerNameLabel references the owning object
	OwnerNameLabel = "local.storage.openshift.io/owner-name"

	// DiskMakerImageEnv is used by the operator to read the DISKMAKER_IMAGE from the environment
	DiskMakerImageEnv = "DISKMAKER_IMAGE"
	// KubeRBACProxyImageEnv is used by the operator to read the KUBE_RBAC_PROXY_IMAGE from the environment
	KubeRBACProxyImageEnv = "KUBE_RBAC_PROXY_IMAGE"
	// LocalDiskLocationEnv is passed to the operator to override the LOCAL_DISK_LOCATION host directory
	LocalDiskLocationEnv = "LOCAL_DISK_LOCATION"

	// ProvisionerConfigMapName is the name of the local-static-provisioner configmap
	ProvisionerConfigMapName = "local-provisioner"

	// DiscoveryNodeLabelKey is the label key on the discovery result CR used to identify the node it belongs to.
	// the value is the node's name
	DiscoveryNodeLabel = "discovery-result-node"

	LocalVolumeStorageClassTemplate     = "templates/localvolume-storageclass.yaml"
	LocalProvisionerConfigMapTemplate   = "templates/local-provisioner-configmap.yaml"
	DiskMakerManagerDaemonSetTemplate   = "templates/diskmaker-manager-daemonset.yaml"
	DiskMakerDiscoveryDaemonSetTemplate = "templates/diskmaker-discovery-daemonset.yaml"
	MetricsServiceTemplate              = "templates/localmetrics/service.yaml"
	MetricsServiceMonitorTemplate       = "templates/localmetrics/service-monitor.yaml"

	// DiskMakerServiceName is the name of the service created for the diskmaker daemon
	DiskMakerServiceName = "local-storage-diskmaker-metrics"

	// DiscoveryServiceName is the name of the service created for the diskmaker discovery daemon
	DiscoveryServiceName = "local-storage-discovery-metrics"

	// DiskMakerMetricsServingCert is the name of secret created for diskmaker service to store TLS config
	DiskMakerMetricsServingCert = "diskmaker-metric-serving-cert"

	// DiscoveryMetricsServingCert is the name of secret created for discovery service to store TLS config
	DiscoveryMetricsServingCert = "discovery-metric-serving-cert"
)

// GetDiskMakerImage returns the image to be used for diskmaker daemonset
func GetDiskMakerImage() string {
	if diskMakerImageFromEnv := os.Getenv(DiskMakerImageEnv); diskMakerImageFromEnv != "" {
		return diskMakerImageFromEnv
	}
	return defaultDiskMakerImageVersion
}

// GetKubeRBACProxyImage returns the image to be used for Kube RBAC Proxy sidecar container
func GetKubeRBACProxyImage() string {
	if kubeRBACProxyImageFromEnv := os.Getenv(KubeRBACProxyImageEnv); kubeRBACProxyImageFromEnv != "" {
		return kubeRBACProxyImageFromEnv
	}
	return defaultKubeProxyImage
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
