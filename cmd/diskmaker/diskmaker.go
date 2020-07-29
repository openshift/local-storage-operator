package main

import (
	"runtime"

	"flag"

	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/prometheus/common/log"
	"github.com/spf13/cobra"
	"k8s.io/klog"
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

func startDiskMaker(cmd *cobra.Command, args []string) error {
	diskMaker, err := diskmaker.NewDiskMaker(configLocation, symlinkLocation)
	if err != nil {
		log.Error(err, "Failed to create DiskMaker")
		return err
	}
	diskMaker.Run(signals.SetupSignalHandler())
	return nil
}
