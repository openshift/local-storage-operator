package diskmaker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	checkDuration = 5 * time.Second
)

type DiskMaker struct {
	configLocation  string
	symlinkLocation string
}

// DiskMaker returns a new instance of DiskMaker
func NewDiskMaker(configLocation, symLinkLocation string) *DiskMaker {
	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
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

	err := os.MkdirAll(d.symlinkLocation, 0755)
	if err != nil {
		logrus.Errorf("error creating local-storage directory %s with %v", d.symlinkLocation, err)
		os.Exit(-1)
	}

	for {
		select {
		case <-ticker.C:
			diskConfig, err := d.loadConfig()
			if err != nil {
				logrus.Errorf("error loading configuration with %v", err)
				break
			}
			d.symLinkDisks(diskConfig)
		case <-stop:
			logrus.Infof("exiting, received message on stop channel")
			os.Exit(0)
		}
	}
}

func (d *DiskMaker) symLinkDisks(diskConfig DiskConfig) {
	cmd := exec.Command("lsblk", "--json")
	var out bytes.Buffer
	var err error
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logrus.Errorf("error running lsblk %v", err)
		return
	}
	deviceSet, err := d.findNewDisks(out.Bytes())
	if err != nil {
		logrus.Errorf("error unmrashalling json %v", err)
		return
	}

	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet)
	if err != nil {
		logrus.Errorf("error matching finding disks : %v", err)
		return
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceName := range deviceArray {
			symLinkDirPath := path.Join(d.symlinkLocation, storageClass)
			err := os.MkdirAll(symLinkDirPath, 0755)
			if err != nil {
				logrus.Errorf("error creating symlink directory %s with %v", symLinkDirPath, err)
				continue
			}
			symLinkPath := path.Join(symLinkDirPath, deviceName)
			devicePath := path.Join("/dev", deviceName)
			symLinkErr := os.Symlink(devicePath, symLinkPath)
			if symLinkErr != nil {
				logrus.Errorf("error creating symlink %s with %v", symLinkPath, err)
			}
		}
	}

}

func (d *DiskMaker) findMatchingDisks(diskConfig DiskConfig, deviceSet sets.String) (map[string][]string, error) {
	blockDeviceMap := make(map[string][]string)
	addDiskToMap := func(scName, diskName string) {
		deviceArray, ok := blockDeviceMap[scName]
		if !ok {
			deviceArray = []string{}
		}
		deviceArray = append(deviceArray, diskName)
		blockDeviceMap[scName] = deviceArray
	}
	for blockDevice := range deviceSet {
		for storageClass, disks := range diskConfig {
			if hasExactDisk(disks.DiskNames, blockDevice) {
				addDiskToMap(storageClass, blockDevice)
				break
			}

			if hasMatchingDisk(disks.DiskPatterns, blockDevice) {
				addDiskToMap(storageClass, blockDevice)
				break
			}
		}
	}
	return blockDeviceMap, nil
}

func (d *DiskMaker) findNewDisks(content []byte) (sets.String, error) {
	deviceSet := sets.NewString()
	blockDeviceMap, unmarshalErr := d.parseBlockJSON(content)
	if unmarshalErr != nil {
		return deviceSet, unmarshalErr
	}
	deviceArray, ok := blockDeviceMap["blockdevices"]
	if !ok {
		return deviceSet, fmt.Errorf("can not find block devices")
	}
	for _, device := range deviceArray {
		deviceSet.Insert(device.Name)
	}
	return deviceSet, nil
}

func (d *DiskMaker) parseBlockJSON(content []byte) (BlockDeviceMap, error) {
	var blockDeviceMap BlockDeviceMap
	unmarshalErr := json.Unmarshal(content, &blockDeviceMap)
	if unmarshalErr != nil {
		return blockDeviceMap, fmt.Errorf("error unmarshalling lsblk output %v", unmarshalErr)
	}
	return blockDeviceMap, nil
}

func hasExactDisk(disks []string, device string) bool {
	for _, disk := range disks {
		if disk == device {
			return true
		}
	}
	return false
}

func hasMatchingDisk(diskPatterns []string, device string) bool {
	for _, diskPattern := range diskPatterns {
		patternExp, err := regexp.Compile(diskPattern)
		if err != nil {
			logrus.Errorf("error compiling disk pattern %s with %v", diskPattern, err)
			continue
		}
		if patternExp.MatchString(device) {
			return true
		}
	}
	return false
}
