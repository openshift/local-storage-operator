package tls

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	libapiserver "github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	libevents "github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetTLSProfileValues fetches the cluster API Server TLS profile via
// library-go's ObserveTLSSecurityProfile and returns TLS_MIN_VERSION and TLS_CIPHER_SUITES
// Returns empty strings on error so callers fall back to kube-rbac-proxy defaults.
func GetTLSProfileValues(ctx context.Context, c client.Client) (minVersion, cipherSuites string, err error) {
	listers := &apiServerListers{&clientAPIServerLister{client: c, ctx: ctx}}
	// InMemoryRecorder captures change events without logging them on every reconcile.
	recorder := libevents.NewInMemoryRecorder("tls-profile", clock.RealClock{})

	observedConfig, errs := libapiserver.ObserveTLSSecurityProfile(listers, recorder, nil)
	if len(errs) > 0 {
		return "", "", fmt.Errorf("failed to observe TLS security profile: %v", errs[0])
	}

	minVersion, _, err = unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion")
	if err != nil {
		return "", "", fmt.Errorf("failed to extract minTLSVersion from observed config: %w", err)
	}

	suites, _, err := unstructured.NestedStringSlice(observedConfig, "servingInfo", "cipherSuites")
	if err != nil {
		return "", "", fmt.Errorf("failed to extract cipherSuites from observed config: %w", err)
	}

	return minVersion, strings.Join(suites, ","), nil
}

// clientAPIServerLister implements configlistersv1.APIServerLister backed by a
// controller-runtime client, bridging the informer-based lister interface that
// ObserveTLSSecurityProfile expects.
type clientAPIServerLister struct {
	client client.Client
	ctx    context.Context
}

func (l *clientAPIServerLister) List(selector labels.Selector) ([]*configv1.APIServer, error) {
	list := &configv1.APIServerList{}
	if err := l.client.List(l.ctx, list); err != nil {
		return nil, err
	}
	result := make([]*configv1.APIServer, 0, len(list.Items))
	for i := range list.Items {
		if selector.Matches(labels.Set(list.Items[i].Labels)) {
			result = append(result, &list.Items[i])
		}
	}
	return result, nil
}

func (l *clientAPIServerLister) Get(name string) (*configv1.APIServer, error) {
	obj := &configv1.APIServer{}
	if err := l.client.Get(l.ctx, types.NamespacedName{Name: name}, obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// apiServerListers satisfies both configobserver.Listers (the declared parameter
// type of ObserveTLSSecurityProfile) and libapiserver.APIServerLister (what the
// type assertion inside ObserveTLSSecurityProfile requires).
// Only APIServerLister() is called by ObserveTLSSecurityProfile; the rest are stubs.
type apiServerListers struct {
	lister *clientAPIServerLister
}

func (a *apiServerListers) APIServerLister() configlistersv1.APIServerLister {
	return a.lister
}

func (a *apiServerListers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return nil
}

func (a *apiServerListers) PreRunHasSynced() []cache.InformerSynced {
	return nil
}
