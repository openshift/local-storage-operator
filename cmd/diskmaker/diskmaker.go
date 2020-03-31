package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"flag"

	"github.com/openshift/local-storage-operator/pkg/apis"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/prometheus/common/log"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	configLocation  string
	symlinkLocation string
	version         = "unknown"
)

func init() {
	flag.StringVar(&configLocation, "config", "/etc/local-storage-operator/config/diskMakerConfig", "location where config map that contains disk maker configuration is mounted")
	flag.StringVar(&symlinkLocation, "local-disk-location", "/mnt/local-storage", "location where local disks should be symlinked")
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("local-storage-diskmaker Version: %v", version)
}

func main() {
	klogFlags := flag.NewFlagSet("local-storage-diskmaker", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	flag.Set("alsologtostderr", "true")
	flag.Parse()

	printVersion()

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Set default manager options
	options := manager.Options{
		Namespace:          namespace,
		MetricsBindAddress: fmt.Sprintf("0"),
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(namespace, ","))
	}

	// Create a new manager to provide shared dependencies and start components
	mgr, err := manager.New(cfg, options)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	diskMaker := diskmaker.NewDiskMaker(configLocation, symlinkLocation, mgr)
	stopChannel := signals.SetupSignalHandler()
	go diskMaker.Run(stopChannel)

	// Start the Cmd
	if err := mgr.Start(stopChannel); err != nil {
		log.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}
