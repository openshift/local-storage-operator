package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newFakeDeviceLinkClient builds a controller-runtime fake client registered
// with the v1 scheme (which includes LocalVolumeDeviceLink).
// WithStatusSubresource is set so that the fake client enforces the status
// subresource boundary: client.Update() will not persist status fields, and
// only client.Status().Update() will — matching real cluster behaviour.
func newFakeDeviceLinkClient(t *testing.T, objs ...runtime.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding v1 to scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1.LocalVolumeDeviceLink{}).
		WithRuntimeObjects(objs...)
}

// helperCommandBlkid returns a fake ExecCommand that makes blkid emit uuid.
func helperCommandBlkid(uuid string) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("COMMAND=%s", command),
			fmt.Sprintf("BLKIDOUT=%s", uuid),
			fmt.Sprintf("GOCOVERDIR=%s", os.TempDir()),
		}
		return cmd
	}
}

// helperCommandBlkidEmpty returns a fake ExecCommand where blkid produces no
// output (device has no filesystem UUID). The TestHelperProcess helper exits 0
// for any unrecognised COMMAND value, producing empty stdout.
func helperCommandBlkidEmpty() func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			"COMMAND=blkid_noop", // unrecognised → exits 0 with empty stdout
			fmt.Sprintf("GOCOVERDIR=%s", os.TempDir()),
		}
		return cmd
	}
}

func TestDeviceLinkHandler_Create_New(t *testing.T) {
	client := newFakeDeviceLinkClient(t).Build()
	handler := NewDeviceLinkHandler("/dev/disk/by-id/wwn-current", "/dev/disk/by-id/wwn-preferred", client)

	pvName := "local-pv-abc123"
	namespace := "default"

	lvdl, err := handler.Create(context.TODO(), pvName, namespace)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if lvdl == nil {
		t.Fatal("Create returned nil LVDL")
	}

	assert.Equal(t, pvName, lvdl.Name)
	assert.Equal(t, namespace, lvdl.Namespace)
	assert.Equal(t, pvName, lvdl.Spec.PersistentVolumeName)
	assert.Equal(t, v1.DeviceLinkPolicyNone, lvdl.Spec.Policy)

	// Verify the object was actually persisted in the fake store.
	fetched := &v1.LocalVolumeDeviceLink{}
	if err := client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: namespace}, fetched); err != nil {
		t.Fatalf("Get after Create failed: %v", err)
	}
	assert.Equal(t, pvName, fetched.Spec.PersistentVolumeName)
}

func TestDeviceLinkHandler_Create_Idempotent(t *testing.T) {
	existing := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-pv-existing",
			Namespace: "default",
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: "local-pv-existing",
			Policy:               v1.DeviceLinkPolicyNone,
		},
	}
	client := newFakeDeviceLinkClient(t, existing).Build()
	handler := NewDeviceLinkHandler("", "", client)

	lvdl, err := handler.Create(context.TODO(), "local-pv-existing", "default")
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	assert.Equal(t, "local-pv-existing", lvdl.Spec.PersistentVolumeName)

	// Exactly one object should exist.
	list := &v1.LocalVolumeDeviceLinkList{}
	if err := client.List(context.TODO(), list); err != nil {
		t.Fatalf("List failed: %v", err)
	}
	assert.Len(t, list.Items, 1)
}

func TestDeviceLinkHandler_Create_UpdatesExistingPVName(t *testing.T) {
	// Simulate an existing LVDL with a stale PersistentVolumeName and a
	// user-set policy. Create should reset Policy to None and update the name.
	existing := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-pv-new",
			Namespace: "default",
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: "local-pv-old",
			Policy:               v1.DeviceLinkPolicyCurrentLinkTarget,
		},
	}
	client := newFakeDeviceLinkClient(t, existing).Build()
	handler := NewDeviceLinkHandler("", "", client)

	lvdl, err := handler.Create(context.TODO(), "local-pv-new", "default")
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	assert.Equal(t, "local-pv-new", lvdl.Spec.PersistentVolumeName)
	assert.Equal(t, v1.DeviceLinkPolicyNone, lvdl.Spec.Policy)
}

func TestDeviceLinkHandler_UpdateStatusAndPV_PopulatesStatus(t *testing.T) {
	pvName := "local-pv-statustest"
	namespace := "default"
	currentSymlink := "/dev/disk/by-id/wwn-current"
	preferredSymlink := "/dev/disk/by-id/wwn-preferred"
	fakeUUID := "550e8400-e29b-41d4-a716-446655440000"
	kname := "sda"
	devPath := filepath.Join("/dev", kname)

	// Pre-create the LVDL that UpdateStatusAndPV will update.
	existing := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{Name: pvName, Namespace: namespace},
		Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: pvName},
	}
	fakeClient := newFakeDeviceLinkClient(t, existing).Build()

	// Create two fake by-id symlinks in a temp dir that resolve to our device.
	tmpByID := t.TempDir()
	link1 := filepath.Join(tmpByID, "wwn-0x1234")
	link2 := filepath.Join(tmpByID, "scsi-abcde")
	if err := os.Symlink(devPath, link1); err != nil {
		t.Fatalf("creating link1: %v", err)
	}
	if err := os.Symlink(devPath, link2); err != nil {
		t.Fatalf("creating link2: %v", err)
	}

	// Override package-level globals for this test.
	origGlob := FilePathGlob
	origEval := FilePathEvalSymLinks
	origExec := ExecCommand
	t.Cleanup(func() {
		FilePathGlob = origGlob
		FilePathEvalSymLinks = origEval
		ExecCommand = origExec
	})

	FilePathGlob = func(pattern string) ([]string, error) {
		return []string{link1, link2}, nil
	}
	FilePathEvalSymLinks = func(path string) (string, error) {
		return devPath, nil
	}
	ExecCommand = helperCommandBlkid(fakeUUID)

	handler := NewDeviceLinkHandler(currentSymlink, preferredSymlink, fakeClient)
	handler.lvdlName = pvName
	handler.namespace = namespace

	updated, err := handler.UpdateStatusAndPV(context.TODO(), kname, devPath)
	if err != nil {
		t.Fatalf("UpdateStatusAndPV returned unexpected error: %v", err)
	}

	assert.Equal(t, currentSymlink, updated.Status.CurrentLinkTarget)
	assert.Equal(t, preferredSymlink, updated.Status.PreferredLinkTarget)
	assert.Equal(t, fakeUUID, updated.Status.FilesystemUUID)
	assert.ElementsMatch(t, []string{link1, link2}, updated.Status.ValidLinkTargets)

	// Verify the status was persisted in the fake store.
	fetched := &v1.LocalVolumeDeviceLink{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: namespace}, fetched); err != nil {
		t.Fatalf("Get after UpdateStatusAndPV failed: %v", err)
	}
	assert.Equal(t, currentSymlink, fetched.Status.CurrentLinkTarget)
	assert.Equal(t, preferredSymlink, fetched.Status.PreferredLinkTarget)
	assert.Equal(t, fakeUUID, fetched.Status.FilesystemUUID)
}

func TestDeviceLinkHandler_UpdateStatusAndPV_NoByIDLinks(t *testing.T) {
	pvName := "local-pv-nolinks"
	namespace := "default"
	currentSymlink := "/dev/sdb"

	existing := &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{Name: pvName, Namespace: namespace},
		Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: pvName},
	}
	fakeClient := newFakeDeviceLinkClient(t, existing).Build()

	origGlob := FilePathGlob
	origExec := ExecCommand
	t.Cleanup(func() {
		FilePathGlob = origGlob
		ExecCommand = origExec
	})

	// No by-id symlinks exist for this device.
	FilePathGlob = func(pattern string) ([]string, error) {
		return []string{}, nil
	}
	ExecCommand = helperCommandBlkidEmpty()

	handler := NewDeviceLinkHandler(currentSymlink, "", fakeClient)
	handler.lvdlName = pvName
	handler.namespace = namespace

	updated, err := handler.UpdateStatusAndPV(context.TODO(), "sdb", "/dev/sdb")
	if err != nil {
		t.Fatalf("UpdateStatusAndPV returned unexpected error: %v", err)
	}

	assert.Equal(t, currentSymlink, updated.Status.CurrentLinkTarget)
	assert.Empty(t, updated.Status.PreferredLinkTarget)
	assert.Empty(t, updated.Status.ValidLinkTargets)
	assert.Empty(t, updated.Status.FilesystemUUID)
}

func TestDeviceLinkHandler_CreateThenUpdateStatus_FullFlow(t *testing.T) {
	pvName := "local-pv-fullflow"
	namespace := "openshift-local-storage"
	currentSymlink := "/dev/disk/by-id/scsi-current"
	preferredSymlink := "/dev/disk/by-id/wwn-preferred"
	kname := "sdc"
	devPath := "/dev/" + kname

	fakeClient := newFakeDeviceLinkClient(t).Build()

	// Step 1: Create.
	handler := NewDeviceLinkHandler(currentSymlink, preferredSymlink, fakeClient)
	lvdl, err := handler.Create(context.TODO(), pvName, namespace)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	assert.Equal(t, pvName, lvdl.Name)
	assert.Equal(t, pvName, lvdl.Spec.PersistentVolumeName)

	// Step 2: UpdateStatusAndPV — no by-id links, no filesystem UUID.
	origGlob := FilePathGlob
	origExec := ExecCommand
	t.Cleanup(func() {
		FilePathGlob = origGlob
		ExecCommand = origExec
	})

	FilePathGlob = func(pattern string) ([]string, error) { return nil, nil }
	ExecCommand = helperCommandBlkidEmpty()

	updated, err := handler.UpdateStatusAndPV(context.TODO(), kname, devPath)
	if err != nil {
		t.Fatalf("UpdateStatusAndPV failed: %v", err)
	}

	assert.Equal(t, currentSymlink, updated.Status.CurrentLinkTarget)
	assert.Equal(t, preferredSymlink, updated.Status.PreferredLinkTarget)
	assert.Empty(t, updated.Status.ValidLinkTargets)
	assert.Empty(t, updated.Status.FilesystemUUID)

	// Confirm the full state is persisted.
	fetched := &v1.LocalVolumeDeviceLink{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: namespace}, fetched); err != nil {
		t.Fatalf("Get after full flow failed: %v", err)
	}
	assert.Equal(t, currentSymlink, fetched.Status.CurrentLinkTarget)
	assert.Equal(t, preferredSymlink, fetched.Status.PreferredLinkTarget)
}
