package discovery

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"syscall"
	"time"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/diskmaker"
	"github.com/openshift/local-storage-operator/diskmaker/controllers/lvset"
	"github.com/openshift/local-storage-operator/internal"
	"github.com/openshift/local-storage-operator/localmetrics"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
)

const (
	localVolumeDiscoveryComponent = "auto-discover-devices"
	udevEventPeriod               = 5 * time.Second
	probeInterval                 = 5 * time.Minute
	resultCRName                  = "discovery-result-%s"
)

var supportedDeviceTypes = sets.NewString("disk", "part", "lvm", "mpath")

// DeviceDiscovery instance
type DeviceDiscovery struct {
	apiClient            diskmaker.ApiUpdater
	eventSync            *diskmaker.EventReporter
	disks                []v1alpha1.DiscoveredDevice
	localVolumeDiscovery *v1alpha1.LocalVolumeDiscovery
}

// NewDeviceDiscovery returns a new DeviceDiscovery instance
func NewDeviceDiscovery() (*DeviceDiscovery, error) {
	scheme := scheme.Scheme
	v1.AddToScheme(scheme)
	v1alpha1.AddToScheme(scheme)
	//api.AddToScheme(scheme)
	apiUpdater, err := diskmaker.NewAPIUpdater(scheme)
	if err != nil {
		klog.Error(err, "failed to create new APIUpdater")
		return &DeviceDiscovery{}, err
	}

	dd := &DeviceDiscovery{}
	dd.apiClient = apiUpdater
	dd.eventSync = diskmaker.NewEventReporter(dd.apiClient)
	lvd, err := dd.apiClient.GetLocalVolumeDiscovery(localVolumeDiscoveryComponent, os.Getenv("WATCH_NAMESPACE"))
	if err != nil {
		klog.Error(err, "failed to get LocalVolumeDiscovery object")
		return &DeviceDiscovery{}, err
	}
	dd.localVolumeDiscovery = lvd
	return dd, nil
}

// Start the device discovery process
func (discovery *DeviceDiscovery) Start() error {
	klog.Info("starting device discovery")
	err := discovery.ensureDiscoveryResultCR()
	if err != nil {
		message := "failed to start device discovery"
		e := diskmaker.NewEvent(diskmaker.ErrorCreatingDiscoveryResultObject, fmt.Sprintf("%s. Error: %+v", message, err), "")
		discovery.eventSync.Report(e, discovery.localVolumeDiscovery)
		return errors.Wrapf(err, message)
	}

	err = discovery.discoverDevices()
	if err != nil {
		errors.Wrapf(err, "failed to discover devices")
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
		message := "failed to discover devices"
		e := diskmaker.NewEvent(diskmaker.ErrorListingBlockDevices, fmt.Sprintf("%s. Error: %+v", message, err), "")
		discovery.eventSync.Report(e, discovery.localVolumeDiscovery)
		return errors.Wrapf(err, message)
	}

	klog.Infof("valid block devices: %+v", validDevices)

	discoveredDisks := getDiscoverdDevices(validDevices)
	klog.Infof("discovered devices: %+v", discoveredDisks)

	// update discovered devices metric
	localmetrics.SetDiscoveredDevicesMetrics(os.Getenv("MY_NODE_NAME"), len(discoveredDisks))

	// Update discovered devices in the  LocalVolumeDiscoveryResult resource
	if !reflect.DeepEqual(discovery.disks, discoveredDisks) {
		klog.Info("device list updated. Updating LocalVolumeDiscoveryResult status...")
		discovery.disks = discoveredDisks
		err = discovery.updateStatus()
		if err != nil {
			message := "failed to update LocalVolumeDiscoveryResult status"
			e := diskmaker.NewEvent(diskmaker.ErrorUpdatingDiscoveryResultObject, fmt.Sprintf("%s. Error: %+v", message, err), "")
			discovery.eventSync.Report(e, discovery.localVolumeDiscovery)
			return errors.Wrapf(err, message)
		}
		message := "successfully updated discovered device details in the LocalVolumeDiscoveryResult resource"
		e := diskmaker.NewSuccessEvent(diskmaker.UpdatedDiscoveredDeviceList, message, "")
		discovery.eventSync.Report(e, discovery.localVolumeDiscovery)
	}

	return nil
}

// getValidBlockDevices fetchs all the block devices sutitable for discovery
func getValidBlockDevices() ([]internal.BlockDevice, error) {
	blockDevices, output, err := internal.ListBlockDevices()
	if err != nil {
		return blockDevices, errors.Wrapf(err, "failed to list all the block devices in the node, stderr=%v", output)
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
			klog.Warningf("failed to get persistent ID for the device %q. Error %v", blockDevice.Name, err)
			deviceID = ""
		}

		size, err := strconv.ParseInt(blockDevice.Size, 10, 64)
		if err != nil {
			klog.Warningf("failed to parse size for the device %q. Error %v", blockDevice.Name, err)
		}

		path, err := blockDevice.GetDevPath()
		if err != nil {
			klog.Warningf("failed to parse path for the device %q. Error %v", blockDevice.KName, err)
		}
		discoveredDevice := v1alpha1.DiscoveredDevice{
			Path:     path,
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

	return uniqueDevices(discoveredDevices)
}

// uniqueDevices removes duplicate devices from the list using DeviceID as a key
// TODO: remove this and use lsblk with -M flag once base images are updated and lsblk v2.34 or higher is available
// See: https://github.com/util-linux/util-linux/blob/3be31a106c52e093928afbea2cddbdbe44cfb357/Documentation/releases/v2.34-ReleaseNotes#L18
func uniqueDevices(sample []v1alpha1.DiscoveredDevice) []v1alpha1.DiscoveredDevice {
	var unique []v1alpha1.DiscoveredDevice
	type key struct{ value string }
	m := make(map[key]int)
	for _, v := range sample {
		k := key{v.DeviceID}
		if i, ok := m[k]; ok {
			unique[i] = v
		} else {
			m[k] = len(unique)
			unique = append(unique, v)
		}
	}
	return unique
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

	if !supportedDeviceTypes.Has(dev.Type) {
		klog.Infof("ignoring device %q with invalid type %q", dev.Name, dev.Type)
		return true
	}

	return false
}

// getDeviceStatus returns device status as "Available", "NotAvailable" or "Unkown"
func getDeviceStatus(dev internal.BlockDevice) v1alpha1.DeviceStatus {
	status := v1alpha1.DeviceStatus{}
	if dev.FSType != "" {
		klog.Infof("device %q with filesystem %q is not available", dev.Name, dev.FSType)
		status.State = v1alpha1.NotAvailable
		return status
	}

	noBiosBootInPartLabel, err := lvset.FilterMap["noBiosBootInPartLabel"](dev, nil)
	if err != nil {
		status.State = v1alpha1.Unknown
		return status
	}
	if !noBiosBootInPartLabel {
		klog.Infof("device %q with part label %q is not available", dev.Name, dev.PartLabel)
		status.State = v1alpha1.NotAvailable
		return status
	}

	canOpen, err := lvset.FilterMap["canOpenExclusively"](dev, nil)
	if err != nil {
		status.State = v1alpha1.Unknown
		return status
	}
	if !canOpen {
		klog.Infof("device %q is not available as it can't be opened exclusively", dev.Name)
		status.State = v1alpha1.NotAvailable
		return status
	}

	hasBindMounts, mountPoint, err := dev.HasBindMounts()
	if err != nil {
		status.State = v1alpha1.Unknown
		return status
	}

	if hasBindMounts {
		klog.Infof("device %q with mount point %q is not available", dev.Name, mountPoint)
		status.State = v1alpha1.NotAvailable
		return status
	}

	klog.Infof("device %q is available", dev.Name)
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

func parseDeviceType(deviceType string) v1alpha1.DiscoveredDeviceType {
	switch {
	case deviceType == "disk":
		return v1alpha1.DiskType
	case deviceType == "part":
		return v1alpha1.PartType
	case deviceType == "lvm":
		return v1alpha1.LVMType
	case deviceType == "mpath":
		return v1alpha1.MultiPathType
	}

	return ""
}
