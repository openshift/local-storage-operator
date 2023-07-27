package common

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

// EnqueueOnlyLabeledSubcomponents returns a predicate that filters only objects that
// have labels["app"] in components
func EnqueueOnlyLabeledSubcomponents(components ...string) predicate.Predicate {

	return predicate.Predicate(predicate.Funcs{
		GenericFunc: func(e event.GenericEvent) bool { return appLabelIn(e.Object, components) },
		CreateFunc:  func(e event.CreateEvent) bool { return appLabelIn(e.Object, components) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return appLabelIn(e.ObjectOld, components) || appLabelIn(e.ObjectNew, components)
		},
		DeleteFunc: func(e event.DeleteEvent) bool { return appLabelIn(e.Object, components) },
	})
}

func appLabelIn(meta metav1.Object, components []string) bool {
	labels := meta.GetLabels()
	appName, found := labels["app"]
	if !found {
		return false
	}
	for _, validName := range components {
		if appName == validName {
			return true
		}
	}
	return false

}

// InitMapIfNil allocates memory to a map if it is nil
func InitMapIfNil(m *map[string]string) {
	if len(*m) > 1 {
		return
	}
	*m = make(map[string]string)
	return
}

// GetNodeNameEnvVar returns the node name from env vars
func GetNodeNameEnvVar() string {
	return os.Getenv("MY_NODE_NAME")
}

// GetWatchNamespace returns the namespace the operator should be watching for changes
func GetWatchNamespace() (string, error) {
	ns, found := os.LookupEnv("WATCH_NAMESPACE")
	if !found {
		return "", fmt.Errorf("%s must be set", "WATCH_NAMESPACE")
	}
	return ns, nil
}

// ReloadRuntimeConfig obtains all values needed by runtime config during Reconcile and writes them to the existing RuntimeConfig provided
func ReloadRuntimeConfig(ctx context.Context, client client.Client, request ctrl.Request, nodeName string, rc *staticProvisioner.RuntimeConfig) error {
	// get associated provisioner config
	cm := &corev1.ConfigMap{}
	err := client.Get(ctx, types.NamespacedName{Name: ProvisionerConfigMapName, Namespace: request.Namespace}, cm)
	if err != nil {
		klog.ErrorS(err, "could not get provisioner configmap", "name", ProvisionerConfigMapName, "namespace", request.Namespace)
		return err
	}

	// get current node
	node := &corev1.Node{}
	err = client.Get(ctx, types.NamespacedName{Name: nodeName}, node)
	if err != nil {
		klog.ErrorS(err, "could not get current Node", "name", nodeName, "namespace", request.Namespace)
		return err
	}

	// read provisioner config
	provisionerConfig := staticProvisioner.ProvisionerConfiguration{}
	if err := staticProvisioner.ConfigMapDataToVolumeConfig(cm.Data, &provisionerConfig); err != nil {
		klog.ErrorS(err, "could not load provisioner config from ConfigMap")
		return err
	}

	rc.Name = GetProvisionedByValue(*node)
	rc.Node = node
	rc.Namespace = request.Namespace
	rc.DiscoveryMap = provisionerConfig.StorageClassConfig
	rc.NodeLabelsForPV = provisionerConfig.NodeLabelsForPV
	rc.SetPVOwnerRef = provisionerConfig.SetPVOwnerRef
	rc.UseNodeNameOnly = provisionerConfig.UseNodeNameOnly
	rc.MinResyncPeriod = provisionerConfig.MinResyncPeriod
	rc.UseAlphaAPI = provisionerConfig.UseAlphaAPI
	rc.LabelsForPV = provisionerConfig.LabelsForPV

	// unsupported
	rc.UseJobForCleaning = false

	return nil

}
