package lvset

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

func TestEventReporer(t *testing.T) {

	uniqueEvents := []diskmaker.DiskEvent{
		newDiskEvent(diskmaker.ErrorRunningBlockList, "running lsblk failed", "/dev/sdb", corev1.EventTypeWarning),
		newDiskEvent(diskmaker.ErrorReadingBlockList, "parsing lsblk failed", "/dev/sdb", corev1.EventTypeWarning),
		newDiskEvent(diskmaker.ErrorListingDeviceID, "couldn't list deviceID", "/dev/sdc", corev1.EventTypeWarning),
		newDiskEvent(diskmaker.ErrorFindingMatchingDisk, "no matching device", "/dev/sdc", corev1.EventTypeWarning),
		newDiskEvent(diskmaker.ErrorCreatingSymLink, "couldn't create symlink", "/dev/sde", corev1.EventTypeWarning),
		newDiskEvent(diskmaker.FoundMatchingDisk, "Found matching disk", "/dev/sdf", corev1.EventTypeNormal),
		newDiskEvent("asdf", "Found matching disk", "/dev/sdf", corev1.EventTypeNormal),
	}
	fakeRecorder := record.NewFakeRecorder(len(uniqueEvents) * 8)
	eventChannel := fakeRecorder.Events
	fakeReporter := newEventReporter(fakeRecorder)

	lvSet := &localv1alpha1.LocalVolumeSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lvset-1",
			Namespace: "default",
		},
	}

	for _, event := range uniqueEvents {
		fakeReporter.Report(lvSet, event)
		// duplicate immediately after
		fakeReporter.Report(lvSet, event)
	}
	for i, event := range uniqueEvents {
		// duplicate again
		fakeReporter.Report(lvSet, event)

		// with different message
		e := event
		event.Message = fmt.Sprintf("something different: %d", i)
		fakeReporter.Report(lvSet, e)
		// reverse order
		e = uniqueEvents[len(uniqueEvents)-(i+1)]
		event.Message = fmt.Sprintf("something differently different: %d", i)
		fakeReporter.Report(lvSet, e)
	}

	recordedEvents := make([]string, 0)
	for len(eventChannel) > 0 {
		var e string
		e = <-eventChannel
		t.Logf("Event Found: %v\n", e)
		if e != "" {
			recordedEvents = append(recordedEvents, e)
		}
	}
	assert.Len(t, recordedEvents, len(uniqueEvents))

}
