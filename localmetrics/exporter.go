package localmetrics

import (
	"bytes"
	"context"
	"fmt"

	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/common"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Exporter struct {
	Ctx             context.Context
	Client          client.Client
	Name            string
	Namespace       string
	OwnerRefs       []metav1.OwnerReference
	Labels          map[string]string
	ServiceCertName string
}

func NewExporter(ctx context.Context, client client.Client, name, namespace, certName string, ownerRefs []metav1.OwnerReference, labels map[string]string) *Exporter {
	return &Exporter{
		Ctx:             ctx,
		Client:          client,
		Name:            name,
		Namespace:       namespace,
		OwnerRefs:       ownerRefs,
		Labels:          labels,
		ServiceCertName: certName,
	}
}

// EnableMetricsExporter creates service and servicemonitor
func (e *Exporter) EnableMetricsExporter() error {
	err := e.enableService()
	if err != nil {
		return err
	}
	err = e.enableServiceMonitor()
	if err != nil {
		return err
	}
	return nil
}

func (e *Exporter) enableService() error {
	service, err := getMetricsService()
	if err != nil {
		return fmt.Errorf("failed to get service. %v", err)
	}

	service.SetName(e.Name)
	service.SetNamespace(e.Namespace)
	service.SetLabels(e.Labels)
	service.SetOwnerReferences(e.OwnerRefs)
	service.Spec.Selector = e.Labels
	service.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = e.ServiceCertName

	if _, err = e.createOrUpdateService(service); err != nil {
		return fmt.Errorf("failed to enable service monitor. %v", err)
	}

	return nil
}

func (e *Exporter) enableServiceMonitor() error {
	serviceMonitor, err := getMetricsServiceMonitor()
	if err != nil {
		return fmt.Errorf("failed to get service monitor. %v", err)
	}

	serviceMonitor.SetName(e.Name)
	serviceMonitor.SetNamespace(e.Namespace)
	serviceMonitor.SetLabels(e.Labels)
	serviceMonitor.SetOwnerReferences(e.OwnerRefs)
	serviceMonitor.Spec.NamespaceSelector.MatchNames = []string{e.Namespace}
	serviceMonitor.Spec.Selector.MatchLabels = e.Labels
	serviceMonitor.Spec.Endpoints[0].TLSConfig.ServerName = fmt.Sprintf("%s.%s.svc", e.Name, e.Namespace)

	if _, err = e.createOrUpdateServiceMonitor(serviceMonitor); err != nil {
		return fmt.Errorf("failed to enable service monitor. %v", err)
	}

	return nil
}

// createOrUpdateService creates service object or an error
func (e *Exporter) createOrUpdateService(service *corev1.Service) (*corev1.Service, error) {
	namespacedName := types.NamespacedName{Namespace: service.GetNamespace(), Name: service.GetName()}
	log := logf.Log.WithName("metrics-exporter")
	log.WithValues("service.namespace", service.GetNamespace(), "service.name", service.GetName())
	log.Info("Reconciling metrics exporter service")

	oldService := &corev1.Service{}
	err := e.Client.Get(e.Ctx, namespacedName, oldService)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = e.Client.Create(e.Ctx, service)
			if err != nil {
				return nil, fmt.Errorf("failed to create metrics exporter service %v. %v", namespacedName, err)
			}
			return service, nil
		}
		return nil, fmt.Errorf("failed to retrieve metrics exporter service %v. %v", namespacedName, err)
	}
	service.ResourceVersion = oldService.ResourceVersion
	service.Spec.ClusterIP = oldService.Spec.ClusterIP
	err = e.Client.Update(e.Ctx, service)
	if err != nil {
		return nil, fmt.Errorf("failed to update service %v. %v", namespacedName, err)
	}
	return service, nil
}

// createOrUpdateServiceMonitor creates serviceMonitor object or an error
func (e *Exporter) createOrUpdateServiceMonitor(serviceMonitor *monitoringv1.ServiceMonitor) (*monitoringv1.ServiceMonitor, error) {
	namespacedName := types.NamespacedName{Name: serviceMonitor.Name, Namespace: serviceMonitor.Namespace}
	log := logf.Log.WithName("service-monitor")
	log.WithValues("service-monitor.namespace", serviceMonitor.GetNamespace(), "serviceMonitor.name", serviceMonitor.GetName())
	log.Info("creating service monitor")

	oldSm := &monitoringv1.ServiceMonitor{}
	err := e.Client.Get(context.TODO(), namespacedName, oldSm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = e.Client.Create(context.TODO(), serviceMonitor)
			if err != nil {
				return nil, fmt.Errorf("failed to get servicemonitor %v. %v", namespacedName, err)
			}
			return serviceMonitor, nil
		}
		return nil, fmt.Errorf("failed to retrieve servicemonitor %v. %v", namespacedName, err)
	}
	oldSm.Spec = serviceMonitor.Spec
	err = e.Client.Update(context.TODO(), oldSm)
	if err != nil {
		return nil, fmt.Errorf("failed to update servicemonitor %v. %v", namespacedName, err)
	}
	return serviceMonitor, nil
}

func getMetricsServiceMonitor() (*monitoringv1.ServiceMonitor, error) {
	file, err := assets.ReadFile(common.MetricsServiceMonitorTemplate)
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

func getMetricsService() (*corev1.Service, error) {
	file, err := assets.ReadFile(common.MetricsServiceTemplate)
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
