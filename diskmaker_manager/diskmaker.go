package main

import (
	"runtime"

	"flag"

	"k8s.io/klog/v2"
)

var (
	symlinkLocation string
	version         = "unknown"
)

func init() {
	flag.StringVar(&symlinkLocation, "local-disk-location", "/mnt/local-storage", "location where local disks should be symlinked")
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("local-storage-diskmaker Version: %v", version)
}
