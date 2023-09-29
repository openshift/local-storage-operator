package common

const (
	// KiB is is 1024 bytes
	KiB int64 = 1024
	// MiB is is 1024 KiB
	MiB = 1024 * KiB
	// GiB is 1024 MiB
	GiB = 1024 * MiB
	// TiB is 1024 GiB
	TiB = 1024 * GiB
)

// RoundDownCapacityPretty rounds down to either the closest GiB or Mib if the resulting value is more than 10 of the respective unit.
func RoundDownCapacityPretty(capacityBytes int64) int64 {

	easyToReadUnitsBytes := []int64{GiB, MiB}

	// Round down to the nearest easy to read unit
	// such that there are at least 10 units at that size.
	for _, easyToReadUnitBytes := range easyToReadUnitsBytes {
		// Round down the capacity to the nearest unit as int64 discards the decimals.
		size := capacityBytes / easyToReadUnitBytes
		if size >= 10 {
			return size * easyToReadUnitBytes
		}
	}
	return capacityBytes
}
