package main

import (
	"context"
	"flag"
	"runtime"
	"time"

	"github.com/openshift/local-storage-operator/pkg/controller"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	corev1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog"
)

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("operator-sdk Version: %v", sdkVersion.Version)
}

func main() {
	klogFlags := flag.NewFlagSet("local-storage-operator", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	flag.Set("alsologtostderr", "true")
	flag.Parse()

	printVersion()

	sdk.ExposeMetricsPort()

	resource := "local.storage.openshift.io/v1"
	kind := "LocalVolume"
	namespace, err := k8sutil.GetWatchNamespace()
	klog.Infof("Watching %s, %s", resource, kind)
	if err != nil {
		klog.Fatalf("failed to get watch namespace: %v", err)
	}
	resyncPeriod := time.Duration(5) * time.Second
	klog.Infof("Watching %s, %s, %s, %d", resource, kind, namespace, resyncPeriod)
	sdk.Watch(resource, kind, namespace, resyncPeriod)
	sdk.Watch("v1", "ConfigMap", namespace, resyncPeriod)
	sdk.Watch("apps/v1", "DaemonSet", namespace, resyncPeriod)
	sdk.Watch("storage.k8s.io/v1", "StorageClass", corev1.NamespaceAll, resyncPeriod)
	sdk.Handle(controller.NewHandler(namespace))
	sdk.Run(context.TODO())
}
