package diskmaker

import (
	"testing"
)

func TestParseBlockJSON(t *testing.T) {
	data := getData()
	d := NewDiskMaker("/tmp/foo", "/mnt/local-storage")
	deviceMap, err := d.parseBlockJSON(data)
	if err != nil {
		t.Errorf("error parsing json %v", err)
	}
	devices := deviceMap["blockdevices"]
	if len(devices) == 0 {
		t.Fatalf("error parsing json, expected devices got empty")
	}
	device1 := devices[0]
	if device1.Name != "sda" {
		t.Errorf("expected device to be sda got %s", device1.Name)
	}
}

func TestFindMatchingDisk(t *testing.T) {
	d := NewDiskMaker("/tmp/foo", "/mnt/local-storage")
	deviceSet, err := d.findNewDisks(getData())
	if err != nil {
		t.Fatalf("error getting data %v", err)
	}
	if len(deviceSet) != 7 {
		t.Errorf("expected 7 devices got %d", len(deviceSet))
	}
	diskConfig := map[string]*Disks{
		"foo": &Disks{
			DiskNames: []string{"vda"},
		},
		"bar": &Disks{
			DiskPatterns: []string{"vd*"},
		},
	}
	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet)
	if err != nil {
		t.Fatalf("error finding matchin device %v", err)
	}
	if len(deviceMap) != 2 {
		t.Errorf("expected 2 elements in map got %d", len(deviceMap))
	}
}

func getData() []byte {
	return []byte(`
{
   "blockdevices": [      {"name": "sda", "maj:min": "8:0", "rm": "0", "size": "32G", "ro": "0", "type": "disk", "mountpoint": null,
         "children": [            {"name": "sda1", "maj:min": "8:1", "rm": "0", "size": "487M", "ro": "0", "type": "part", "mountpoint": "/boot"},
            {"name": "sda2", "maj:min": "8:2", "rm": "0", "size": "4G", "ro": "0", "type": "part", "mountpoint": "[SWAP]"},
            {"name": "sda3", "maj:min": "8:3", "rm": "0", "size": "27.5G", "ro": "0", "type": "part", "mountpoint": "/"}
         ]
      },
      {"name": "vda", "maj:min": "253:0", "rm": "0", "size": "4G", "ro": "1", "type": "disk", "mountpoint": null},
      {"name": "vdb", "maj:min": "253:16", "rm": "0", "size": "2G", "ro": "0", "type": "disk", "mountpoint": "/var/lib/kubelet/pods/146fdbe9-c5c8-11e8-a383-52540000926c/volumes/kubernetes.io~local-volume/local-pv-2aaef0c1"},
      {"name": "vdc", "maj:min": "253:32", "rm": "0", "size": "2G", "ro": "0", "type": "disk", "mountpoint": "/var/lib/kubelet/pods/17fb073c-c5c8-11e8-a383-52540000926c/volumes/kubernetes.io~local-volume/local-pv-9c13f484"},
      {"name": "vdd", "maj:min": "253:48", "rm": "0", "size": "2G", "ro": "0", "type": "disk", "mountpoint": "/var/lib/kubelet/pods/33130ba1-c5c8-11e8-a383-52540000926c/volumes/kubernetes.io~local-volume/local-pv-24b1687f"},
      {"name": "vde", "maj:min": "253:64", "rm": "0", "size": "2G", "ro": "0", "type": "disk", "mountpoint": null},
      {"name": "vdf", "maj:min": "253:80", "rm": "0", "size": "2G", "ro": "0", "type": "disk", "mountpoint": null}
   ]}
`)
}
