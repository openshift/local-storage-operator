package diskmaker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os/exec"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
)

var (
	checkDuration = 5 * time.Second
)

type DiskMaker struct {
	diskMakerConfig *DiskConfig
	configLocation  string
	symlinkLocation string
}

// DiskMaker returns a new instance of DiskMaker
func NewDiskMaker() *DiskMaker {
	t := &DiskMaker{}
	t.configLocation = "/etc/local-operator/config/diskMakerConfig"
	t.symlinkLocation = "/mnt/local-storage"
	return t
}

func (d *DiskMaker) loadConfig() (DiskConfig, error) {
	var err error
	content, err := ioutil.ReadFile(d.configLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s with %v", d.configLocation, err)
	}
	var diskConfig DiskConfig
	err = yaml.Unmarshal(content, &diskConfig)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling %s with %v", d.configLocation, err)
	}
	return diskConfig, nil
}

// Run and create disk config
func (d *DiskMaker) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			diskConfig, err := d.loadConfig()
			if err != nil {
				logrus.Errorf("error loading configuration with %v", err)
				break
			}
			d.detectDisks(diskConfig)
		case <-stop:
			logrus.Infof("exiting, received message on stop channel")
		}
	}
}

func (d *DiskMaker) detectDisks(diskConfig DiskConfig) {
	deviceArray, err := d.findNewDisks()
	if err != nil {
		logrus.Errorf("Error finding new disks %v", err)
		return
	}

}

func (d *DiskMaker) findNewDisks() (DeviceArray, error) {
	var deviceArray DeviceArray
	cmd := exec.Command("lsblk", "--json")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return deviceArray, fmt.Errorf("error running lsblk %v", err)
	}
	var blockDeviceMap BlockDeviceMap
	unmarshalErr := json.Unmarshal(out.Bytes(), &blockDeviceMap)
	if unmarshalErr != nil {
		return deviceArray, fmt.Errorf("error unmarshalling lsblk output %v", unmarshalErr)
	}
	deviceArray, ok := blockDeviceMap["blockdevices"]
	if !ok {
		return deviceArray, fmt.Errorf("can not find block devices")
	}
	return deviceArray, nil
}
