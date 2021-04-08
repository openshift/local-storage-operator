package localmetrics

import (
	"context"
	"fmt"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	portMetrics    = "metrics"
	portExporter   = "exporter"
	metricsPath    = "/metrics"
	metricsPort    = "8383"
	scrapeInterval = "1m"
)

type Exporter struct {
	Client    client.Client
	Name      string
	Namespace string
	OwnerRefs []metav1.OwnerReference
	Labels    map[string]string
}

func NewExporter(client client.Client, name, namespace string, ownerRefs []metav1.OwnerReference, labels map[string]string) *Exporter {
	return &Exporter{
		Client:    client,
		Name:      name,
		Namespace: namespace,
		OwnerRefs: ownerRefs,
		Labels:    labels,
	}
}

// EnableMetricsExporter is a wrapper around createOrUpdateService()
// and createOrUpdateServiceMonitor()
func (e *Exporter) EnableMetricsExporter() error {
	_, err := e.createOrUpdateService()
	if err != nil {
		return err
	}
	_, err = e.createOrUpdateServiceMonitor()
	if err != nil {
		return err
	}
	return nil
}

func (e *Exporter) getMetricsExporterService() *corev1.Service {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            e.Name,
			Namespace:       e.Namespace,
			Labels:          e.Labels,
			OwnerReferences: e.OwnerRefs,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:     portMetrics,
					Port:     int32(8383),
					Protocol: corev1.ProtocolTCP,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: int32(8383),
						StrVal: "8383",
					},
				},
				{
					Name:     portExporter,
					Port:     int32(8081),
					Protocol: corev1.ProtocolTCP,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: int32(8081),
						StrVal: "8081",
					},
				},
			},
			Selector: e.Labels,
		},
	}
	return service
}

// createOrUpdateService creates service object or an error
func (e *Exporter) createOrUpdateService() (*corev1.Service, error) {
	service := e.getMetricsExporterService()
	namespacedName := types.NamespacedName{Namespace: service.GetNamespace(), Name: service.GetName()}

	log.Info("Reconciling metrics exporter service", "NamespacedName", namespacedName)

	oldService := &corev1.Service{}
	err := e.Client.Get(context.TODO(), namespacedName, oldService)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = e.Client.Create(context.TODO(), service)
			if err != nil {
				return nil, fmt.Errorf("failed to create metrics exporter service %v. %v", namespacedName, err)
			}
			return service, nil
		}
		return nil, fmt.Errorf("failed to retrieve metrics exporter service %v. %v", namespacedName, err)
	}
	service.ResourceVersion = oldService.ResourceVersion
	service.Spec.ClusterIP = oldService.Spec.ClusterIP
	err = e.Client.Update(context.TODO(), service)
	if err != nil {
		return nil, fmt.Errorf("failed to update service %v. %v", namespacedName, err)
	}
	return service, nil
}

func (e *Exporter) getMetricsExporterServiceMonitor() *monitoringv1.ServiceMonitor {
	serviceMonitor := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:            e.Name,
			Namespace:       e.Namespace,
			Labels:          e.Labels,
			OwnerReferences: e.OwnerRefs,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{e.Namespace},
			},
			Selector: metav1.LabelSelector{
				MatchLabels: e.Labels,
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     portMetrics,
					Path:     metricsPath,
					Interval: scrapeInterval,
				},
				{
					Port:     portExporter,
					Path:     metricsPath,
					Interval: scrapeInterval,
				},
			},
		},
	}
	return serviceMonitor
}

// createOrUpdateServiceMonitor creates serviceMonitor object or an error
func (e *Exporter) createOrUpdateServiceMonitor() (*monitoringv1.ServiceMonitor, error) {
	serviceMonitor := e.getMetricsExporterServiceMonitor()
	namespacedName := types.NamespacedName{Name: serviceMonitor.Name, Namespace: serviceMonitor.Namespace}

	log.Info("Reconciling metrics exporter service monitor", "NamespacedName", namespacedName)

	oldSm := &monitoringv1.ServiceMonitor{}
	err := e.Client.Get(context.TODO(), namespacedName, oldSm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = e.Client.Create(context.TODO(), serviceMonitor)
			if err != nil {
				return nil, fmt.Errorf("failed to create metrics exporter servicemonitor %v. %v", namespacedName, err)
			}
			return serviceMonitor, nil
		}
		return nil, fmt.Errorf("failed to retrieve metrics exporter servicemonitor %v. %v", namespacedName, err)
	}
	oldSm.Spec = serviceMonitor.Spec
	err = e.Client.Update(context.TODO(), oldSm)
	if err != nil {
		return nil, fmt.Errorf("failed to update metrics exporter servicemonitor %v. %v", namespacedName, err)
	}
	return serviceMonitor, nil
}
