package localmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricDiscoveredDevicesByLocalVolumeDiscovery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lso_discovery_disk_count",
		Help: "Total disks discovered by the Local Volume Discovery controller per node",
	}, []string{"nodeName"})

	LVDMetricsList = []prometheus.Collector{
		metricDiscoveredDevicesByLocalVolumeDiscovery,
	}
)

func SetDiscoveredDevicesMetrics(nodeName string, deviceCount int) {
	metricDiscoveredDevicesByLocalVolumeDiscovery.
		With(prometheus.Labels{"nodeName": nodeName}).
		Set(float64((deviceCount)))
}
