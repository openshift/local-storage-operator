package localmetrics

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/pkg/common"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Exporter struct {
	Ctx             context.Context
	Client          client.Client
	Name            string
	Namespace       string
	OwnerRefs       []metav1.OwnerReference
	AppLabel        string
	ServiceCertName string
}

func NewExporter(ctx context.Context, client client.Client, name, namespace, certName string, ownerRefs []metav1.OwnerReference, appLabel string) *Exporter {
	return &Exporter{
		Ctx:             ctx,
		Client:          client,
		Name:            name,
		Namespace:       namespace,
		OwnerRefs:       ownerRefs,
		AppLabel:        appLabel,
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
	replacer := strings.NewReplacer(
		"${OBJECT_NAME}", e.Name,
		"${OBJECT_NAMESPACE}", e.Namespace,
		"${APP_LABEL}", e.AppLabel,
		"${SERVICE_CERT_NAME}", e.ServiceCertName,
	)

	service, err := getMetricsService(replacer)
	if err != nil {
		return fmt.Errorf("failed to get service. %v", err)
	}

	service.SetOwnerReferences(e.OwnerRefs)

	if _, err = e.createOrUpdateService(service); err != nil {
		return fmt.Errorf("failed to enable service monitor. %v", err)
	}

	return nil
}

func (e *Exporter) enableServiceMonitor() error {
	replacer := strings.NewReplacer(
		"${OBJECT_NAME}", e.Name,
		"${OBJECT_NAMESPACE}", e.Namespace,
		"${APP_LABEL}", e.AppLabel,
		"${SERVICE_CERT_NAME}", e.ServiceCertName,
	)

	serviceMonitor, err := getMetricsServiceMonitor(replacer)
	if err != nil {
		return fmt.Errorf("failed to get service monitor. %v", err)
	}

	serviceMonitor.SetOwnerReferences(e.OwnerRefs)

	if _, err = e.createOrUpdateServiceMonitor(serviceMonitor); err != nil {
		return fmt.Errorf("failed to enable service monitor. %v", err)
	}

	return nil
}

// createOrUpdateService creates service object or an error
func (e *Exporter) createOrUpdateService(service *corev1.Service) (*corev1.Service, error) {
	namespacedName := types.NamespacedName{Namespace: service.GetNamespace(), Name: service.GetName()}
	klog.InfoS("Reconciling metrics service", "namespace", service.GetNamespace(), "name", service.GetName())

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
	klog.InfoS("Reconciling metrics service monitor", "namespace", serviceMonitor.GetNamespace(), "name", serviceMonitor.GetName())

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

func getMetricsServiceMonitor(replacer *strings.Replacer) (*monitoringv1.ServiceMonitor, error) {
	file, err := assets.ReadFile(common.MetricsServiceMonitorTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service monitor file. %v", err)
	}

	serviceMonitorYaml := replacer.Replace(string(file))

	var servicemonitor monitoringv1.ServiceMonitor
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(serviceMonitorYaml), 1000).Decode(&servicemonitor)
	if err != nil {
		return nil, fmt.Errorf("failed to decode service monitor")
	}
	return &servicemonitor, nil
}

func getMetricsService(replacer *strings.Replacer) (*corev1.Service, error) {
	file, err := assets.ReadFile(common.MetricsServiceTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service monitor file. %v", err)
	}

	serviceYaml := replacer.Replace(string(file))
	var service corev1.Service
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(serviceYaml), 1000).Decode(&service)
	if err != nil {
		return nil, fmt.Errorf("failed to decode service monitor")
	}
	return &service, nil
}
