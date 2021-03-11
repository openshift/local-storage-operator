package events

import (
	"fmt"
	"testing"
	"time"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/record"
)

type FakeEventRecorder struct {
	Events chan string
}

func NewFakeEventReporter() (*EventReporter, chan string, *clock.FakeClock) {
	// set up fake reporter
	fakeRecorder := record.NewFakeRecorder(20)
	eventChannel := fakeRecorder.Events
	// set up fake time at epoch
	fakeClock := clock.NewFakeClock(time.Time{})

	// set up fake eventReporter
	eventReporter := EventReporter{
		Recorder:       fakeRecorder,
		clock:          fakeClock,
		reportedEvents: make(map[string]time.Time),
	}
	return &eventReporter, eventChannel, fakeClock
}

func countStringOccurences(channel chan string, foundEvents *map[string]int) {
	for len(channel) > 0 {
		var e string
		e = <-channel
		val, found := (*foundEvents)[e]
		if !found {
			val = 0
		}
		(*foundEvents)[e] = val + 1

	}
	return
}

func TestReconcileEvent(t *testing.T) {
	eventReporter, eventChannel, fakeClock := NewFakeEventReporter()

	// localvolume object
	lv := &localv1.LocalVolume{ObjectMeta: metav1.ObjectMeta{Name: "lv1", Namespace: "ns1"}, TypeMeta: metav1.TypeMeta{Kind: localv1.LocalVolumeKind}}

	type step struct {
		duration    time.Duration
		shouldOccur bool
	}

	tt := []struct {
		event KeyedEvent
		steps []step
	}{
		{
			event: NewReconcileEvent("ReconcileFailed", "api failure", corev1.EventTypeWarning),
			steps: []step{
				{
					duration:    0,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 14,
					shouldOccur: false,
				},
				{
					duration:    time.Minute * 16,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 29,
					shouldOccur: false,
				},
				{
					duration:    time.Minute * 200,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 28,
					shouldOccur: false,
				},
				{
					duration:    time.Minute * 4,
					shouldOccur: true,
				},
			},
		},
		{
			event: NewReconcileEvent("ReconcileFailed", "not permitted", corev1.EventTypeWarning),
			steps: []step{
				{
					duration:    time.Minute * 400,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 400,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 30,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 29,
					shouldOccur: false,
				},
				{
					duration:    time.Minute * 29,
					shouldOccur: true,
				},
				{
					duration:    time.Minute * 2,
					shouldOccur: false,
				},
			},
		},
	}

	for testNum, tc := range tt {
		ev := tc.event
		// key as fake recorder implements it
		key := fmt.Sprintf("%s %s %s", ev.GetType(), ev.GetReason(), ev.GetMessage())
		for stepNum, step := range tc.steps {
			fakeClock.Step(step.duration)

			err := eventReporter.ReportKeyedEvent(lv, ev)
			assert.Nil(t, err)

			// intialize keyCountMap
			keyCountMap := make(map[string]int, 0)
			countStringOccurences(eventChannel, &keyCountMap)

			_, found := keyCountMap[key]
			assert.Equalf(t, step.shouldOccur, found, "test #%d, step #%d", testNum, stepNum)
		}
	}

}
