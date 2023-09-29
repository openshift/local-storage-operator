package main

import (
	"github.com/openshift/local-storage-operator/pkg/diskmaker/discovery"
	"github.com/openshift/local-storage-operator/pkg/localmetrics"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func startDeviceDiscovery(cmd *cobra.Command, args []string) error {
	printVersion()
	// start local server to emit custom metrics
	err := localmetrics.NewConfigBuilder().
		WithCollectors(localmetrics.LVDMetricsList).
		Build()
	if err != nil {
		return errors.Wrap(err, "failed to discover devices")
	}

	discoveryObj, err := discovery.NewDeviceDiscovery()
	if err != nil {
		return errors.Wrap(err, "failed to discover devices")
	}

	err = discoveryObj.Start()
	if err != nil {
		return errors.Wrap(err, "failed to discover devices")
	}

	return nil
}
