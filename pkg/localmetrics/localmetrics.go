package localmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	log = logf.Log.WithName("localmetrics")
)

var (
	metricPVProvisionedByLocalVolume = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_provisioned_pvs",
		Help: "Report how many persistent volumes have been provisoned by Local Volume Operator",
	}, []string{"nodeName", "storageClass"})

	metricPVProvisionedByLocalVolumeSet = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_set_provisioned_pvs",
		Help: "Report how many persistent volumes have been provisoned by Local Volume Operator",
	}, []string{"storageClass"})

	metricDiscoveredDevicesByLocalVolumeDiscovery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_discovery_discovered_disks",
		Help: "Report how many disks were discoverd via the Local Volume Discovery Operator",
	}, []string{"nodeName"})

	metricUnmatchedDevicesByLocalVolumeSet = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_set_unmatched_disks",
		Help: "Report how many disks didn't match the Local Volume Set filter",
	}, []string{"nodeName", "storageClass"})

	metricOrphanSynlinksByLocalVolumeSet = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_set_orphaned_symlinks",
		Help: "Report how many synlinks became orphan after updating the Local Volume Set filter",
	}, []string{"nodeName", "storageClass"})

	metricOrphanSynlinksByLocalVolume = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "local_volume_orphaned_symlinks",
		Help: "Report how many synlinks became orphan after updating the Local Volume device filter",
	}, []string{"nodeName", "storageClass"})

	LVMetricsList = []prometheus.Collector{
		metricPVProvisionedByLocalVolume,
		metricOrphanSynlinksByLocalVolume,
	}
	LVDMetricsList = []prometheus.Collector{
		metricDiscoveredDevicesByLocalVolumeDiscovery,
	}
	LVSMetricsList = []prometheus.Collector{
		metricPVProvisionedByLocalVolumeSet,
		metricUnmatchedDevicesByLocalVolumeSet,
		metricOrphanSynlinksByLocalVolumeSet,
	}
)

func SetDiscoveredDevicesMetrics(nodeName string, deviceCount int) {
	metricDiscoveredDevicesByLocalVolumeDiscovery.
		With(prometheus.Labels{"nodeName": nodeName}).
		Set(float64((deviceCount)))
}

func SetLVSProvisionedPVs(sc string, pvCount int) {
	metricPVProvisionedByLocalVolumeSet.
		With(prometheus.Labels{"storageClass": sc}).
		Set(float64((pvCount)))
}

func SetLVProvisionedPVs(nodeName, sc string, pvCount int) {
	metricPVProvisionedByLocalVolume.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": sc}).
		Set(float64((pvCount)))
}

func SetLVSUnmatchedDevices(nodeName, sc string, deviceCount int) {
	metricUnmatchedDevicesByLocalVolumeSet.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": sc}).
		Set(float64((deviceCount)))
}

func SetLVSOrphanSymlinks(nodeName, sc string, deviceCount int) {
	metricOrphanSynlinksByLocalVolumeSet.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": sc}).
		Set(float64((deviceCount)))
}

func SetLVOrphanSymlinks(nodeName, sc string, deviceCount int) {
	metricOrphanSynlinksByLocalVolume.
		With(prometheus.Labels{"nodeName": nodeName, "storageClass": sc}).
		Set(float64((deviceCount)))
}

func ConfigureCustomMetrics(collector []prometheus.Collector) {
	metricConfig := NewMetricsConfig(metricsPath, metricsPort, collector)
	if err := metricConfig.startMetricsServer(); err != nil {
		log.Error(err, "Failed to configure custom metrics")
	}
}
