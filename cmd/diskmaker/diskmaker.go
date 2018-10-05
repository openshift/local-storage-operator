package main

import (
	"runtime"

	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
)

var (
	configLocation  string
	symlinkLocation string
)

func init() {
	flag.StringVar(&configLocation, "config", "/etc/local-storage-operator/config/diskMakerConfig", "location where config map that contains disk maker configuration is mounted")
	flag.StringVar(&symlinkLocation, "local-disk-location", "/mnt/local-storage", "location where local disks should be symlinked")
}

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
}

func main() {
	printVersion()
	flag.Parse()
	diskMaker := diskmaker.NewDiskMaker(configLocation, symlinkLocation)
	stopChannel := make(chan struct{})
	diskMaker.Run(stopChannel)
}
