package lvset

import (
	"fmt"
	"testing"
	"time"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/stretchr/testify/assert"
)

type fakeClock struct {
	ftime time.Time
}

func (f *fakeClock) getCurrentTime() time.Time {
	return f.ftime
}

func TestDeviceAge(t *testing.T) {
	// empty the filters and matchers
	oldFilterMap := FilterMap
	FilterMap = make(map[string]func(internal.BlockDevice, *localv1alpha1.DeviceInclusionSpec) (bool, error), 0)

	oldMatcherMap := matcherMap
	matcherMap = make(map[string]func(internal.BlockDevice, *localv1alpha1.DeviceInclusionSpec) (bool, error), 0)

	// reset the filters and matchers
	defer func() {
		FilterMap = oldFilterMap
		matcherMap = oldMatcherMap
	}()

	r, tc := newFakeLocalVolumeSetReconciler(t)

	logger := log.WithName("test-logr")

	blockDevices := make([]internal.BlockDevice, 0)
	// the amount to increment the block devices by between each time duration
	increment := 5

	initial := time.Unix(0, 0) // 0 seconds from epoch

	assert.Lessf(t, int64(time.Second*10), int64(deviceMinAge), "deviceMinAge should be less than 10 seconds")

	runs := []time.Time{
		initial.Add(0),                            // Run 0, add offset number of devices, time is start of epoch
		initial.Add(time.Second * 10),             // Run 1, add offset number of devices, increment time
		initial.Add(deviceMinAge + time.Second*5), // Run 2, expect devices from run 0 to become valid, but not from Run 1
		initial.Add(deviceMinAge * 3),             // Run 4, expect devices from run 0, 1,2 to become valid, but not run 4 (they are just disovered)
	}
	expectedValid := []int{
		0,
		0,
		increment,
		increment * 3,
	}

	for run, atTime := range runs {
		t.Logf("Run %d, time is set to %+v", run, atTime)
		// freeze time
		tc.fakeClock.ftime = atTime
		t.Logf("Adding %v devices at this time ^", increment)
		// initial block devices (Set 0)
		targetLength := len(blockDevices) + increment
		for i := len(blockDevices); i < targetLength; i++ {
			blockDevices = append(blockDevices, internal.BlockDevice{KName: fmt.Sprintf("dev-%d", len(blockDevices))})
		}

		validDevices, delayedDevices := r.getValidDevices(logger, nil, blockDevices)
		assert.Lenf(t, validDevices, expectedValid[run], "validDevices")
		assert.Lenf(t, delayedDevices, len(blockDevices)-expectedValid[run], "delayedDevices")

	}
}
