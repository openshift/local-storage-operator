package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchUdevEvent(t *testing.T) {
	testcases := []struct {
		label     string
		text      string
		matches   []string
		exclusion []string
		expected  bool
	}{
		{
			label:     "case 1",
			text:      "KERNEL[1008.734088] add      /devices/pci0000:00/0000:00:07.0/virtio5/block/vdc (block)",
			matches:   []string{"(?i)add", "(?i)remove"},
			exclusion: []string{"(?i)dm-[0-9]+"},
			expected:  true,
		},
		{
			label:     "case 2",
			text:      "KERNEL[1008.734088] remove     /devices/pci0000:00/0000:00:07.0/virtio5/block/vdc (block)",
			matches:   []string{"(?i)add", "(?i)remove"},
			exclusion: []string{"(?i)dm-[0-9]+"},
			expected:  true,
		},
		{
			label:     "case 3",
			text:      "KERNEL[1008.734088] change      /devices/pci0000:00/0000:00:07.0/virtio5/block/vdc (block)",
			matches:   []string{"(?i)add", "(?i)remove"},
			exclusion: []string{"(?i)dm-[0-9]+"},
			expected:  false,
		},
		{
			label:     "case 4",
			text:      "KERNEL[1042.464238] add      /devices/virtual/block/dm-1 (block)",
			matches:   []string{"(?i)add", "(?i)remove"},
			exclusion: []string{"(?i)dm-[0-9]+"},
			expected:  false,
		},
	}

	for _, tc := range testcases {
		actual, err := matchUdevEvent(tc.text, tc.matches, tc.exclusion)
		assert.NoError(t, err)
		assert.Equalf(t, tc.expected, actual, "[%q] udev event matcher failed", tc.label)
	}
}
