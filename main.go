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
	"flag"
	"fmt"
	"os"
	"runtime"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	lvcontroller "github.com/openshift/local-storage-operator/controllers/localvolume"
	lvdcontroller "github.com/openshift/local-storage-operator/controllers/localvolumediscovery"
	lvscontroller "github.com/openshift/local-storage-operator/controllers/localvolumeset"
	nodedaemoncontroller "github.com/openshift/local-storage-operator/controllers/nodedaemon"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = apiruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(localv1.AddToScheme(scheme))
	utilruntime.Must(localv1alpha1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func printVersion() {
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	setupLog.Info(fmt.Sprintf("local-storage-operator Version: %s", version))
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
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	namespace, err := common.GetWatchNamespace()
	if err != nil {
		setupLog.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Namespace:              namespace,
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "98d5776d.storage.openshift.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&lvcontroller.LocalVolumeReconciler{
		Client: mgr.GetClient(),
		//	Log:    ctrl.Log.WithName("controllers").WithName("LocalVolume"),
		LvMap:  &common.StorageClassOwnerMap{},
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LocalVolume")
		os.Exit(1)
	}
	if err = (&lvdcontroller.LocalVolumeDiscoveryReconciler{
		Client:    mgr.GetClient(),
		ReqLogger: logf.Log.WithName("controllers").WithName("LocalVolumeDiscovery"),
		//	Log:    ctrl.Log.WithName("controllers").WithName("LocalVolumeDiscovery"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LocalVolumeDiscovery")
		os.Exit(1)
	}
	if err = (&lvscontroller.LocalVolumeSetReconciler{
		Client:    mgr.GetClient(),
		ReqLogger: logf.Log.WithName("controllers").WithName("LocalVolumeSet"),
		//	Log:    ctrl.Log.WithName("controllers").WithName("LocalVolumeSet"),
		LvSetMap: &common.StorageClassOwnerMap{},
		Scheme:   mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LocalVolumeSet")
		os.Exit(1)
	}

	if err = (&nodedaemoncontroller.DaemonReconciler{
		Client:    mgr.GetClient(),
		ReqLogger: logf.Log.WithName("controllers").WithName("NodeDaemon"),
		//	Log:    ctrl.Log.WithName("controllers").WithName("LocalVolumeSet"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LocalVolumeSet")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
