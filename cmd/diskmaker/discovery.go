package main

import (
	"github.com/openshift/local-storage-operator/pkg/diskmaker/discovery"
	"github.com/openshift/local-storage-operator/pkg/localmetrics"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func startDeviceDiscovery(cmd *cobra.Command, args []string) error {
	printVersion()
	// configure custom metrics for local volume discovery
	localmetrics.ConfigureCustomMetrics(localmetrics.LVDMetricsList)
	discoveryObj, err := discovery.NewDeviceDiscovery()
	if err != nil {
		return errors.Wrapf(err, "failed to discover devices")
	}
	err = discoveryObj.Start()
	if err != nil {
		return errors.Wrapf(err, "failed to discover devices")
	}
	return nil
}
