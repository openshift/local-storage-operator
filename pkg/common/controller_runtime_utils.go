package common

import (
	"os"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnqueueOnlyLabeledSubcomponents returns a predicate that filters only objects that
// have labels["app"] in components
func EnqueueOnlyLabeledSubcomponents(components ...string) predicate.Predicate {

	return predicate.Predicate(predicate.Funcs{
		GenericFunc: func(e event.GenericEvent) bool { return appLabelIn(e.Meta, components) },
		CreateFunc:  func(e event.CreateEvent) bool { return appLabelIn(e.Meta, components) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return appLabelIn(e.MetaOld, components) || appLabelIn(e.MetaNew, components)
		},
		DeleteFunc: func(e event.DeleteEvent) bool { return appLabelIn(e.Meta, components) },
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

// GetWatchNameSpaceEnfVar returns the watch namespace
func GetWatchNameSpaceEnfVar() string {
	return os.Getenv("WATCH_NAMESPACE")
}
