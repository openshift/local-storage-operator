package main

import (
	"runtime"

	"flag"

	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"k8s.io/klog"
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
	diskMaker := diskmaker.NewDiskMaker(configLocation, symlinkLocation)
	stopChannel := make(chan struct{})
	diskMaker.Run(stopChannel)
}
