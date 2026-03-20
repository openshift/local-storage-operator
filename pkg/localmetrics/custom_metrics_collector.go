package localmetrics

import (
	"context"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// deviceLinkMismatchDesc describes the mismatch gauge emitted per
// LocalVolumeDeviceLink. It carries the policy label so that alert
// expressions can filter on policy="None" without needing a join.
var deviceLinkMismatchDesc = prometheus.NewDesc(
	"lso_device_link_mismatch",
	"1 if currentLinkTarget differs from preferredLinkTarget for a LocalVolumeDeviceLink, 0 otherwise.",
	[]string{"name", "persistent_volume", "policy"},
	nil,
)

// deviceLinkWithoutStablePathDesc describes the gauge emitted per
// LocalVolumeDeviceLink when no /dev/disk/by-id symlinks were
// discovered for the underlying device (validLinkTargets is empty).
var deviceLinkWithoutStablePathDesc = prometheus.NewDesc(
	"lso_device_link_without_stable_path",
	"1 if validLinkTargets is empty for a LocalVolumeDeviceLink (no by-id symlinks discovered), 0 otherwise.",
	[]string{"name", "persistent_volume", "policy"},
	nil,
)

// DeviceLinkCollector implements prometheus.Collector for
// LocalVolumeDeviceLink objects. It lists objects on each scrape via
// the controller-runtime client, which is already backed by an
// informer cache and does not hit the API server.
type DeviceLinkCollector struct {
	client    client.Client
	namespace string
}

// NewDeviceLinkCollector returns a collector scoped to the given namespace.
// Pass an empty string to collect across all namespaces.
func NewDeviceLinkCollector(c client.Client, namespace string) *DeviceLinkCollector {
	return &DeviceLinkCollector{
		client:    c,
		namespace: namespace,
	}
}

// Describe sends the metric descriptors to ch.
func (c *DeviceLinkCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- deviceLinkMismatchDesc
	ch <- deviceLinkWithoutStablePathDesc
}

// Collect lists all LocalVolumeDeviceLink objects and emits
// lso_device_link_mismatch and/or lso_device_link_without_stable_path gauges only
// for objects that have a problem (mismatch or missing by-id symlinks).
// Objects where everything is fine are skipped to avoid unnecessary
// metric cardinality. If the List call fails, invalid metrics are sent
// so that Prometheus records a scrape error.
func (c *DeviceLinkCollector) Collect(ch chan<- prometheus.Metric) {
	list := &v1.LocalVolumeDeviceLinkList{}

	opts := []client.ListOption{}
	if c.namespace != "" {
		opts = append(opts, client.InNamespace(c.namespace))
	}

	if err := c.client.List(context.TODO(), list, opts...); err != nil {
		klog.ErrorS(err, "failed to list LocalVolumeDeviceLink objects for metrics collection")
		ch <- prometheus.NewInvalidMetric(deviceLinkMismatchDesc, err)
		ch <- prometheus.NewInvalidMetric(deviceLinkWithoutStablePathDesc, err)
		return
	}

	for i := range list.Items {
		link := &list.Items[i]

		mismatch := link.Status.CurrentLinkTarget != link.Status.PreferredLinkTarget
		noStablePath := len(link.Status.ValidLinkTargets) == 0

		// Only emit metrics for objects that have a problem to avoid
		// unnecessary metric cardinality in the cluster.
		if !mismatch && !noStablePath {
			continue
		}

		policy := string(link.Spec.Policy)
		if policy == "" {
			policy = string(v1.DeviceLinkPolicyNone)
		}

		if mismatch {
			ch <- prometheus.MustNewConstMetric(
				deviceLinkMismatchDesc,
				prometheus.GaugeValue,
				1,
				link.Name,
				link.Spec.PersistentVolumeName,
				policy,
			)
		}

		if noStablePath {
			ch <- prometheus.MustNewConstMetric(
				deviceLinkWithoutStablePathDesc,
				prometheus.GaugeValue,
				1,
				link.Name,
				link.Spec.PersistentVolumeName,
				policy,
			)
		}
	}
}
