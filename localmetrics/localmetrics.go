package localmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// LocalVolumeDiscovery metrics
	metricDiscoveredDevicesByLocalVolumeDiscovery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_discovery_disk_count",
		Help: "Total disks discovered by the Local Volume Discovery controller per node",
	}, []string{"nodeName"})

	// LocalVolumeSet metrics
	metricLocalVolumeSetProvisionedPVs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_lvset_provisioned_PV_count",
		Help: "Total persistent volumes provisioned by the Local Volume Set controller per node",
	}, []string{"nodeName", "storageClass"})

	metricLocalVolumeSetUnmatchedDisks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_lvset_unmatched_disk_count",
		Help: "Total disks that didn't match the Local Volume Set filter",
	}, []string{"nodeName", "storageClass"})

	metricLocalVolumeSetOrphanedSymlinks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_lvset_orphaned_symlink_count",
		Help: "Total symlinks that became orphan after updating the Local Volume Set filter",
	}, []string{"nodeName", "storageClass"})

	// LocalVolume metrics
	metricLocalVolumeProvisionedPVs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_lv_provisioned_PV_count",
		Help: "Total persistent volumes provisioned by the LocalVolume controller per node",
	}, []string{"nodeName", "storageClass"})

	metricLocalVolumeOrphanedSymlinks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_lv_orphaned_symlink_count",
		Help: "Total symlinks that became orphan after updating the devicePaths in LocalVolume CR",
	}, []string{"nodeName", "storageClass"})

	LVDMetricsList = []prometheus.Collector{
		metricDiscoveredDevicesByLocalVolumeDiscovery,
	}

	LVMetricsList = []prometheus.Collector{
		metricLocalVolumeSetProvisionedPVs,
		metricLocalVolumeSetUnmatchedDisks,
		metricLocalVolumeSetOrphanedSymlinks,
		metricLocalVolumeProvisionedPVs,
		metricLocalVolumeOrphanedSymlinks,
	}
)

func SetDiscoveredDevicesMetrics(nodeName string, deviceCount int) {
	metricDiscoveredDevicesByLocalVolumeDiscovery.
		With(prometheus.Labels{"nodeName": nodeName}).
		Set(float64(deviceCount))
}

func SetLVSProvisionedPVMetric(nodeName, storageClassName string, count int) {
	metricLocalVolumeSetProvisionedPVs.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": storageClassName}).
		Set(float64(count))
}

func SetLVSUnmatchedDiskMetric(nodeName, storageClassName string, count int) {
	metricLocalVolumeSetUnmatchedDisks.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": storageClassName}).
		Set(float64(count))
}

func SetLVSOrphanedSymlinksMetric(nodeName, storageClassName string, count int) {
	metricLocalVolumeSetOrphanedSymlinks.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": storageClassName}).
		Set(float64(count))
}

func SetLVProvisionedPVMetric(nodeName, storageClassName string, count int) {
	metricLocalVolumeProvisionedPVs.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": storageClassName}).
		Set(float64(count))
}

func SetLVOrphanedSymlinksMetric(nodeName, storageClassName string, count int) {
	metricLocalVolumeOrphanedSymlinks.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": storageClassName}).
		Set(float64(count))
}
