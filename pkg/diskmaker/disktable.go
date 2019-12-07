package diskmaker

import "strings"

type blockDevice map[string]string

type diskTable struct {
	disks []blockDevice
}

func newDiskTable() *diskTable {
	d := &diskTable{}
	d.disks = []blockDevice{}
	return d
}

func (d *diskTable) parse(output string) {
	partInfo := strings.Split(output, "\n")
	for _, info := range partInfo {
		props := parseKeyValuePairString(info)
		if len(props) > 0 {
			d.disks = append(d.disks, props)
		}
	}
}

func (d *diskTable) filterUsableDisks() []blockDevice {
	usableDisks := []blockDevice{}
	for _, disk := range d.disks {
		mountPoint := disk["MOUNTPOINT"]
		if len(mountPoint) > 0 {
			continue
		}

		diskName := disk["KNAME"]

		// next we need to check if disk has partitions
		// in which case, we can't use root disk as localdisk
		isRootDisk := false
		for _, disk2 := range d.disks {
			disk2PrimaryDiskName := disk2["PKNAME"]
			if disk2PrimaryDiskName == diskName {
				isRootDisk = true
				break
			}
		}
		if !isRootDisk {
			usableDisks = append(usableDisks, disk)
		}
	}
	return usableDisks
}

// converts a raw key value pair string into a map of key value pairs
// example raw string of `foo="0" bar="1" baz="biz"` is returned as:
// map[string]string{"foo":"0", "bar":"1", "baz":"biz"}
func parseKeyValuePairString(propsRaw string) blockDevice {
	// first split the single raw string on spaces and initialize a map of
	// a length equal to the number of pairs
	props := strings.Split(propsRaw, " ")
	propMap := make(blockDevice)

	for _, kvpRaw := range props {
		// split each individual key value pair on the equals sign
		kvp := strings.Split(kvpRaw, "=")
		if len(kvp) == 2 {
			// first element is the final key, second element is the final value
			// (don't forget to remove surrounding quotes from the value)
			propMap[kvp[0]] = strings.Replace(kvp[1], `"`, "", -1)
		}
	}

	return propMap
}
