package discovery

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/openshift/local-storage-operator/pkg/apis"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker/controllers/lvset"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	udevEventPeriod = 5 * time.Second
	probeInterval   = 5 * time.Minute
	resultCRName    = "discovery-result-%s"
	resultCRLabel   = "discovery-result-node"
)

type DeviceDiscovery struct {
	client client.Client
	disks  []v1alpha1.DiscoveredDevice
}

func NewDeviceDiscovery() (*DeviceDiscovery, error) {
	client, err := getClient()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create DevieDiscovery instance")
	}
	return &DeviceDiscovery{
		client: client,
	}, nil
}

func getClient() (client.Client, error) {
	scheme := scheme.Scheme
	apis.AddToScheme(scheme)
	config, err := config.GetConfig()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get rest.config")
	}
	crClient, err := client.New(config, client.Options{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create controller-runtime client")
	}
	return crClient, nil
}

func (discovery *DeviceDiscovery) Start() error {
	klog.Info("starting device discovery")
	err := discovery.ensureDiscoveryResultCR()
	if err != nil {
		return errors.Wrapf(err, "failed to create LocalVolumeDiscoveryResult resource.")
	}

	err = discovery.discoverDevices()
	if err != nil {
		errors.Wrapf(err, "failed to start device discovery")
	}

	// Watch udev events for continuous discovery of devices
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGTERM)

	udevEvents := make(chan string)
	go udevBlockMonitor(udevEvents, udevEventPeriod)
	for {
		select {
		case <-sigc:
			klog.Info("shutdown signal received, exiting...")
			return nil
		case <-time.After(probeInterval):
			if err := discovery.discoverDevices(); err != nil {
				klog.Errorf("failed to discover devices during probe interval. %v", err)
			}
		case _, ok := <-udevEvents:
			if ok {
				klog.Info("trigger probe from udev event")
				if err := discovery.discoverDevices(); err != nil {
					klog.Errorf("failed to discover devices triggered from udev event. %v", err)
				}
			} else {
				klog.Warningf("disabling udev monitoring")
				udevEvents = nil
			}
		}
	}
}

// discoverDevices identifies the list of usable disks on the current node
func (discovery *DeviceDiscovery) discoverDevices() error {
	// List all the valid block devices on the node
	validDevices, err := getValidBlockDevices()
	if err != nil {
		return errors.Wrapf(err, "failed to discover devices")
	}
	klog.Infof("valid block devices: %+v", validDevices)

	discoveredDisks := getDiscoverdDevices(validDevices)
	klog.Infof("discovered devices: %+v", discoveredDisks)

	// Update discovered devices in the  LocalVolumeDiscoveryResult resource
	if !reflect.DeepEqual(discovery.disks, discoveredDisks) {
		klog.Info("device list updated. Updating LocalVolumeDiscoveryResult status...")
		discovery.disks = discoveredDisks
		err = discovery.updateStatus()
		if err != nil {
			return errors.Wrapf(err, "failed to update status")
		}
		klog.Infof("successfully update discovered device details in the LocalVolumeDiscoveryResult CR")
	}

	return nil
}

// getValidBlockDevices fetchs all the block devices sutitable for discovery
func getValidBlockDevices() ([]internal.BlockDevice, error) {
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		return blockDevices, errors.Wrapf(err, "failed to list all the block devices in the node.")
	} else if len(badRows) > 0 {
		klog.Warningf("failed to parse all the lsblk rows. Bad rows: %+v", badRows)
	}

	// Get valid list of devices
	validDevices := make([]internal.BlockDevice, 0)
	for _, blockDevice := range blockDevices {
		if ignoreDevices(blockDevice) {
			continue
		}
		validDevices = append(validDevices, blockDevice)
	}

	return validDevices, nil
}

// getDiscoverdDevices creates v1alpha1.DiscoveredDevice from internal.BlockDevices
func getDiscoverdDevices(blockDevices []internal.BlockDevice) []v1alpha1.DiscoveredDevice {
	discoveredDevices := make([]v1alpha1.DiscoveredDevice, 0)
	for _, blockDevice := range blockDevices {
		deviceID, err := blockDevice.GetPathByID()
		if err != nil {
			klog.Warningf("failed to get persisent ID for the device %q. Error %v", blockDevice.Name, err)
			deviceID = ""
		}
		size, err := resource.ParseQuantity(blockDevice.Size)
		if err != nil {
			klog.Warningf("failed to parse size for the device %q. Error %v", blockDevice.Name, err)
		}

		discoveredDevice := v1alpha1.DiscoveredDevice{
			Path:     fmt.Sprintf("/dev/%s", blockDevice.Name),
			Model:    blockDevice.Model,
			Vendor:   blockDevice.Vendor,
			FSType:   blockDevice.FSType,
			Serial:   blockDevice.Serial,
			Type:     parseDeviceType(blockDevice.Type),
			DeviceID: deviceID,
			Size:     size,
			Property: parseDeviceProperty(blockDevice.Rotational),
			Status:   getDeviceStatus(blockDevice),
		}
		discoveredDevices = append(discoveredDevices, discoveredDevice)
	}

	return discoveredDevices
}

// ignoreDevices checks if a device should be ignored during discovery
func ignoreDevices(dev internal.BlockDevice) bool {
	if readOnly, err := dev.GetReadOnly(); err != nil || readOnly {
		klog.Infof("ignoring read only device %q", dev.Name)
		return true
	}

	if hasChildren, err := dev.HasChildren(); err != nil || hasChildren {
		klog.Infof("ignoring root device %q", dev.Name)
		return true
	}

	if dev.State == internal.StateSuspended {
		klog.Infof("ignoring device %q with invalid state %q", dev.Name, dev.State)
		return true
	}

	if !(dev.Type == "disk" || dev.Type == "part") {
		klog.Infof("ignoring device %q with invalid type %q", dev.Name, dev.Type)
		return true
	}

	return false
}

// getDeviceStatus returns device status as "Available", "NotAvailable" or "Unkown"
func getDeviceStatus(dev internal.BlockDevice) v1alpha1.DeviceStatus {
	status := v1alpha1.DeviceStatus{}
	if dev.FSType != "" {
		status.State = v1alpha1.NotAvailable
		return status
	}

	canOpen, err := lvset.FilterMap["canOpenExclusively"](dev, nil)
	if err != nil {
		status.State = v1alpha1.Unknown
		return status
	}
	if !canOpen {
		status.State = v1alpha1.NotAvailable
		return status
	}

	status.State = v1alpha1.Available
	return status
}

func parseDeviceProperty(property string) v1alpha1.DeviceMechanicalProperty {
	switch {
	case property == "1":
		return v1alpha1.Rotational
	case property == "0":
		return v1alpha1.NonRotational
	}

	return ""
}

func parseDeviceType(deviceType string) v1alpha1.DeviceType {
	switch {
	case deviceType == "disk":
		return v1alpha1.RawDisk
	case deviceType == "part":
		return v1alpha1.Partition
	}

	return ""
}
