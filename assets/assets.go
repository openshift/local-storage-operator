package assets

import (
	"bytes"
	"embed"
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	metricsServiceAsset        = "localmetrics/service.yaml"
	metricsServiceMonitorAsset = "localmetrics/service-monitor.yaml"
)

//go:embed localmetrics/*.yaml
var f embed.FS

// readFile reads and returns the content of the named file.
func readFile(name string) ([]byte, error) {
	return f.ReadFile(name)
}

func GetMetricsServiceMonitor() (*monitoringv1.ServiceMonitor, error) {
	file, err := readFile(metricsServiceMonitorAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service monitor file. %v", err)
	}

	var servicemonitor monitoringv1.ServiceMonitor
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(string(file)), 1000).Decode(&servicemonitor)
	if err != nil {
		return nil, fmt.Errorf("failed to decode service monitor")
	}
	return &servicemonitor, nil
}

func GetMetricsService() (*corev1.Service, error) {
	file, err := readFile(metricsServiceAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service monitor file. %v", err)
	}

	var service corev1.Service
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(string(file)), 1000).Decode(&service)
	if err != nil {
		return nil, fmt.Errorf("failed to decode service monitor")
	}
	return &service, nil
}
