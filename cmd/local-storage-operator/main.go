/*
Copyright 2021 The Local Storage Operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"runtime"
	"slices"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	crcommon "github.com/openshift/controller-runtime-common/pkg/tls"
	libcrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	libevents "github.com/openshift/library-go/pkg/operator/events"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	lvcontroller "github.com/openshift/local-storage-operator/pkg/controllers/localvolume"
	lvdcontroller "github.com/openshift/local-storage-operator/pkg/controllers/localvolumediscovery"
	lvscontroller "github.com/openshift/local-storage-operator/pkg/controllers/localvolumeset"
	nodedaemoncontroller "github.com/openshift/local-storage-operator/pkg/controllers/nodedaemon"
	lsotls "github.com/openshift/local-storage-operator/pkg/tls"
	"github.com/openshift/local-storage-operator/pkg/utils"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	zaplog "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	//+kubebuilder:scaffold:imports
)

var (
	scheme  = apiruntime.NewScheme()
	version = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(localv1.AddToScheme(scheme))
	utilruntime.Must(localv1alpha1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("local-storage-diskmaker Version: %v", version)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	klogFlags := flag.NewFlagSet("local-storage-operator", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	opts := zap.Options{
		Development: true,
		ZapOpts:     []zaplog.Option{zaplog.AddCaller()},
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}

	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	printVersion()

	namespace, err := common.GetWatchNamespace()
	if err != nil {
		klog.ErrorS(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	restConfig := ctrl.GetConfigOrDie()
	le := utils.GetLeaderElectionConfig(restConfig, enableLeaderElection)

	// Fetch TLS profile and adherence once at startup to ensure consistency
	// Use a timeout to prevent indefinite stalls on API server outages
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	configClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		klog.ErrorS(err, "unable to create config client for TLS profile")
		os.Exit(1)
	}

	adherence, err := crcommon.FetchAPIServerTLSAdherencePolicy(ctx, configClient)
	if err != nil {
		klog.ErrorS(err, "failed to fetch TLS adherence policy")
		os.Exit(1)
	}
	klog.Infof("TLS adherence policy: %s", adherence)

	tlsProfile, err := lsotls.FetchAPIServerTLSProfile(ctx, configClient)
	if err != nil {
		klog.ErrorS(err, "failed to fetch TLS profile")
		os.Exit(1)
	}

	// Resolve whether the TLSGroupPreferences feature gate is enabled. The predefined
	// TLS profiles bake non-empty Groups arrays into them, so without this gate LSO
	// would restrict TLS group (curve) preferences on every cluster as soon as the
	// supporting libraries are in place. Until the feature is GA, curves must stay
	// unrestricted on clusters where the gate is disabled (the default).
	tlsGroupsEnabled := resolveTLSGroupPreferencesEnabled(restConfig)

	var tlsConfigFn func(*tls.Config)
	if libcrypto.ShouldHonorClusterTLSProfile(adherence) {
		// Drop Groups when the feature gate is disabled so the metrics server does
		// not restrict curves (Surface A feature-gate gating). Operate on a copy so
		// the watcher baseline below keeps the full fetched profile; otherwise the
		// watcher would compare a Groups-stripped baseline against the live APIServer
		// profile and trigger a spurious restart on every startup.
		metricsProfile := tlsProfile
		if !tlsGroupsEnabled {
			metricsProfile.Groups = nil
		}
		var unsupportedCiphers []string
		tlsConfigFn, unsupportedCiphers = lsotls.GetTLSConfigFromProfile(metricsProfile)
		if len(unsupportedCiphers) > 0 {
			klog.Warningf("TLS profile contains %d unsupported cipher suites: %v",
				len(unsupportedCiphers), unsupportedCiphers)
		}
		klog.Infof("TLS adherence policy %q: using cluster TLS profile", adherence)
	} else {
		defaultProfile := *configv1.TLSProfiles[libcrypto.DefaultTLSProfileType]
		if !tlsGroupsEnabled {
			defaultProfile.Groups = nil
		}
		var unsupportedCiphers []string
		tlsConfigFn, unsupportedCiphers = lsotls.GetTLSConfigFromProfile(defaultProfile)
		if len(unsupportedCiphers) > 0 {
			klog.Warningf("Default TLS profile contains %d unsupported cipher suites: %v",
				len(unsupportedCiphers), unsupportedCiphers)
		}
		klog.Infof("TLS adherence policy %q: using default Intermediate profile", adherence)
	}

	metricsOpts := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: true,
		TLSOpts:       []func(*tls.Config){tlsConfigFn},
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
		},
		Scheme:                 scheme,
		Metrics:                metricsOpts,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		RenewDeadline:          &le.RenewDeadline.Duration,
		RetryPeriod:            &le.RetryPeriod.Duration,
		LeaseDuration:          &le.LeaseDuration.Duration,
		LeaderElectionID:       "98d5776d.storage.openshift.io",
	})
	if err != nil {
		klog.ErrorS(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&lvcontroller.LocalVolumeReconciler{
		Client: mgr.GetClient(),
		LvMap:  &common.StorageClassOwnerMap{},
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "unable to create LocalVolume controller")
		os.Exit(1)
	}
	if err = (&lvdcontroller.LocalVolumeDiscoveryReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		TLSGroupsEnabled: tlsGroupsEnabled,
	}).SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "unable to create LocalVolumeDiscovery controller")
		os.Exit(1)
	}
	if err = (&lvscontroller.LocalVolumeSetReconciler{
		Client:   mgr.GetClient(),
		LvSetMap: &common.StorageClassOwnerMap{},
		Scheme:   mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "unable to create LocalVolumeSet controller")
		os.Exit(1)
	}

	if err = (&nodedaemoncontroller.DaemonReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		TLSGroupsEnabled: tlsGroupsEnabled,
	}).SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "unable to create NodeDaemon controller")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.ErrorS(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.ErrorS(err, "unable to set up ready check")
		os.Exit(1)
	}

	mgrCtx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	// Setup TLS security profile watcher to trigger graceful shutdown on changes
	tlsWatcher := lsotls.NewSecurityProfileWatcher(tlsProfile, adherence, cancel)
	tlsWatcher.Client = mgr.GetClient()
	if err = tlsWatcher.SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "unable to create TLS security profile watcher")
		os.Exit(1)
	}

	klog.Info("starting manager")
	if err := mgr.Start(mgrCtx); err != nil {
		klog.ErrorS(err, "problem running manager")
		os.Exit(1)
	}
}

// resolveTLSGroupPreferencesEnabled reports whether the TLSGroupPreferences feature
// gate is enabled for the OpenShift version this operator is managing.
//
// LSO is OLM-managed and ships no version env var, so it passes the missingVersion
// marker as the desired version and library-go derives the managed version from the
// ClusterVersion, consistent with the CSI operators (STOR-3176).
//
// library-go's FeatureGateAccess terminates the process when feature gates change,
// so the resolved value is valid for the life of this process; a restart re-resolves
// it. On any error or timeout it returns false, keeping curves unrestricted, which is
// the correct pre-GA default.
func resolveTLSGroupPreferencesEnabled(restConfig *rest.Config) bool {
	const missingVersion = "0.0.1-snapshot"

	configClientset, err := configv1client.NewForConfig(restConfig)
	if err != nil {
		klog.ErrorS(err, "unable to build config clientset for feature gate detection; assuming TLSGroupPreferences disabled")
		return false
	}

	configInformers := configinformers.NewSharedInformerFactory(configClientset, 10*time.Minute)
	recorder := libevents.NewLoggingEventRecorder("local-storage-operator-featuregates", clock.RealClock{})

	featureGateAccess := featuregates.NewFeatureGateAccess(
		missingVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(),
		configInformers.Config().V1().FeatureGates(),
		recorder,
	)
	go featureGateAccess.Run(context.Background())
	configInformers.Start(wait.NeverStop)

	select {
	case <-featureGateAccess.InitialFeatureGatesObserved():
		featureGates, err := featureGateAccess.CurrentFeatureGates()
		if err != nil {
			klog.ErrorS(err, "unable to read feature gates; assuming TLSGroupPreferences disabled")
			return false
		}
		// Enabled panics if the gate is not registered for the resolved version,
		// which happens on releases where TLSGroupPreferences does not exist yet.
		// Treat an unknown gate as disabled, matching the pre-GA default.
		if !slices.Contains(featureGates.KnownFeatures(), features.FeatureGateTLSGroupPreferences) {
			klog.Infof("TLSGroupPreferences feature gate not registered; assuming disabled")
			return false
		}
		enabled := featureGates.Enabled(features.FeatureGateTLSGroupPreferences)
		klog.Infof("TLSGroupPreferences feature gate enabled: %t", enabled)
		return enabled
	case <-time.After(60 * time.Second):
		klog.Error("timed out waiting for feature gates to be observed")
		os.Exit(1)
	}
	return false
}
