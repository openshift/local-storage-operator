package localmetrics

import (
	"context"
	"errors"
	"testing"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// buildFakeClient creates a fake client pre-loaded with the given LVDL objects.
func buildFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme, err := localv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

type lvdlBuilder struct {
	lvdl *localv1.LocalVolumeDeviceLink
}

func (l *lvdlBuilder) makeDeviceLink(name, namespace, pvName string) *lvdlBuilder {
	l.lvdl = &localv1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: localv1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: pvName,
		},
	}
	return l
}

func (l *lvdlBuilder) withPolicy(policy localv1.DeviceLinkPolicy) *lvdlBuilder {
	l.lvdl.Spec.Policy = policy
	return l
}

func (l *lvdlBuilder) withLinkTargets(current, preferred string, validTargets ...string) *lvdlBuilder {
	l.lvdl.Status.CurrentLinkTarget = current
	l.lvdl.Status.PreferredLinkTarget = preferred
	l.lvdl.Status.ValidLinkTargets = validTargets
	return l
}

func (l *lvdlBuilder) build() *localv1.LocalVolumeDeviceLink {
	return l.lvdl
}

// collectMetrics drains the Collect channel into a slice.
func collectMetrics(collector prometheus.Collector) []prometheus.Metric {
	ch := make(chan prometheus.Metric, 100)
	collector.Collect(ch)
	close(ch)
	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	return metrics
}

// findFamily returns the MetricFamily with the given name, or nil.
func findFamily(mfs []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func TestDescribe(t *testing.T) {
	collector := NewDeviceLinkCollector(buildFakeClient(t), "test-ns")
	ch := make(chan *prometheus.Desc, 10)
	collector.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}
	if len(descs) != 2 {
		t.Errorf("expected 2 descriptors, got %d", len(descs))
	}
}

func TestCollect_NoObjects(t *testing.T) {
	collector := NewDeviceLinkCollector(buildFakeClient(t), "test-ns")
	metrics := collectMetrics(collector)
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics for empty client, got %d", len(metrics))
	}
}

func TestCollect_SingleObject(t *testing.T) {
	tests := []struct {
		name           string
		link           *localv1.LocalVolumeDeviceLink
		expectMismatch bool
		expectNoByID   bool
	}{
		{
			name: "healthy object skipped",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-old").
				build(),
			expectMismatch: false,
			expectNoByID:   false,
		},
		{
			name: "no mismatch with explicit policy",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyCurrentLinkTarget).
				withLinkTargets("/dev/disk/by-id/scsi-stable", "/dev/disk/by-id/scsi-stable", "/dev/disk/by-id/scsi-stable").
				build(),
			expectMismatch: false,
			expectNoByID:   false,
		},
		{
			name: "mismatch only",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-new", "/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-new").
				build(),
			expectMismatch: true,
			expectNoByID:   false,
		},
		{
			name: "mismatch with one target empty",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("/dev/disk/by-id/scsi-old", "").
				build(),
			expectMismatch: true,
			expectNoByID:   true,
		},
		{
			name: "both targets empty, no valid links",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("", "").
				build(),
			expectMismatch: false,
			expectNoByID:   true,
		},
		{
			name: "no by-id only",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("/dev/sda", "/dev/sda").
				build(),
			expectMismatch: false,
			expectNoByID:   true,
		},
		{
			name: "mismatch and no by-id",
			link: (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
				withPolicy(localv1.DeviceLinkPolicyNone).
				withLinkTargets("/dev/sda", "/dev/sdb").
				build(),
			expectMismatch: true,
			expectNoByID:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := NewDeviceLinkCollector(buildFakeClient(t, tt.link), "openshift-local-storage")

			reg := prometheus.NewRegistry()
			if err := reg.Register(collector); err != nil {
				t.Fatalf("failed to register collector: %v", err)
			}
			mfs, err := reg.Gather()
			if err != nil {
				t.Fatalf("unexpected gather error: %v", err)
			}

			mismatchFamily := findFamily(mfs, "lso_device_link_mismatch")
			if tt.expectMismatch && mismatchFamily == nil {
				t.Error("expected lso_device_link_mismatch metric but it was not emitted")
			}
			if !tt.expectMismatch && mismatchFamily != nil {
				t.Error("lso_device_link_mismatch metric should not be emitted")
			}

			noByIDFamily := findFamily(mfs, "lso_device_link_without_stable_path")
			if tt.expectNoByID && noByIDFamily == nil {
				t.Error("expected lso_device_link_without_stable_path metric but it was not emitted")
			}
			if !tt.expectNoByID && noByIDFamily != nil {
				t.Error("lso_device_link_without_stable_path metric should not be emitted")
			}
		})
	}
}

func TestCollect_MultipleObjects(t *testing.T) {
	// link1: mismatch + valid targets → 1 mismatch metric
	link1 := (&lvdlBuilder{}).makeDeviceLink("pv-a", "openshift-local-storage", "pv-a").
		withPolicy(localv1.DeviceLinkPolicyNone).
		withLinkTargets("/dev/disk/by-id/old-a", "/dev/disk/by-id/new-a", "/dev/disk/by-id/old-a", "/dev/disk/by-id/new-a").
		build()
	// link2: healthy → skipped
	link2 := (&lvdlBuilder{}).makeDeviceLink("pv-b", "openshift-local-storage", "pv-b").
		withPolicy(localv1.DeviceLinkPolicyPreferredLinkTarget).
		withLinkTargets("/dev/disk/by-id/stable", "/dev/disk/by-id/stable", "/dev/disk/by-id/stable").
		build()
	// link3: no mismatch + no valid targets → 1 no-by-id metric
	link3 := (&lvdlBuilder{}).makeDeviceLink("pv-c", "openshift-local-storage", "pv-c").
		withPolicy(localv1.DeviceLinkPolicyNone).
		withLinkTargets("/dev/sdc", "/dev/sdc").
		build()

	collector := NewDeviceLinkCollector(buildFakeClient(t, link1, link2, link3), "openshift-local-storage")
	metrics := collectMetrics(collector)
	// link1 → 1 mismatch, link2 → 0 (healthy), link3 → 1 no-by-id
	if len(metrics) != 2 {
		t.Errorf("expected 2 metrics (1 mismatch + 1 no-by-id), got %d", len(metrics))
	}
}

func TestCollect_ListError(t *testing.T) {
	scheme, err := localv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	errClient := interceptor.NewClient(base, interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
			return errors.New("simulated list failure")
		},
	})

	collector := NewDeviceLinkCollector(errClient, "test-ns")
	metrics := collectMetrics(collector)
	// Two invalid metrics are emitted when List fails (one per descriptor).
	if len(metrics) != 2 {
		t.Errorf("expected 2 invalid metrics on list error, got %d", len(metrics))
	}
}

func TestCollect_EmptyPolicy(t *testing.T) {
	// Empty policy must be normalised to "None" — need a mismatch to
	// trigger metric emission so we can verify the label.
	link := (&lvdlBuilder{}).makeDeviceLink("pv-node1", "openshift-local-storage", "pv-node1").
		withLinkTargets("/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-new", "/dev/disk/by-id/scsi-old", "/dev/disk/by-id/scsi-new").
		build()
	collector := NewDeviceLinkCollector(buildFakeClient(t, link), "openshift-local-storage")

	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		t.Fatalf("failed to register collector: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("unexpected gather error: %v", err)
	}
	mismatchFamily := findFamily(mfs, "lso_device_link_mismatch")
	if mismatchFamily == nil {
		t.Fatal("lso_device_link_mismatch family not found")
	}
	// Verify the policy label was normalised to "None".
	for _, lp := range mismatchFamily.GetMetric()[0].GetLabel() {
		if lp.GetName() == "policy" && lp.GetValue() != string(localv1.DeviceLinkPolicyNone) {
			t.Errorf("expected policy to be normalised to %q, got %q", localv1.DeviceLinkPolicyNone, lp.GetValue())
		}
	}
}
