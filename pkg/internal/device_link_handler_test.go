package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	v1 "github.com/openshift/local-storage-operator/api/v1"
	v1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
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
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding corev1 to scheme: %v", err)
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

func TestDeviceLinkHandler_Create(t *testing.T) {
	testCases := []struct {
		name             string
		pvName           string
		namespace        string
		currentSymlink   string
		preferredSymlink string
		ownerObj         runtime.Object
		existing         *v1.LocalVolumeDeviceLink
		expectedPolicy   v1.DeviceLinkPolicy
		expectedListLen  int
	}{
		{
			name:             "creates new lvdl",
			pvName:           "local-pv-abc123",
			namespace:        "default",
			currentSymlink:   "/dev/disk/by-id/wwn-current",
			preferredSymlink: "/dev/disk/by-id/wwn-preferred",
			ownerObj: &v1.LocalVolume{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1.LocalVolumeKind,
					APIVersion: v1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lv-a",
					Namespace: "default",
					UID:       types.UID("11111111-2222-3333-4444-555555555555"),
				},
			},
			expectedPolicy:  v1.DeviceLinkPolicyNone,
			expectedListLen: 1,
		},
		{
			name:             "creates new lvdl with localvolumeset ownerref",
			pvName:           "local-pv-lvset-owner",
			namespace:        "default",
			currentSymlink:   "/dev/disk/by-id/scsi-current",
			preferredSymlink: "/dev/disk/by-id/scsi-preferred",
			ownerObj: &v1alpha1.LocalVolumeSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1.LocalVolumeSetKind,
					APIVersion: v1alpha1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lvset-a",
					Namespace: "default",
					UID:       types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
				},
			},
			expectedPolicy:  v1.DeviceLinkPolicyNone,
			expectedListLen: 1,
		},
		{
			name:      "idempotent when object already exists",
			pvName:    "local-pv-existing",
			namespace: "default",
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local-pv-existing",
					Namespace: "default",
				},
				Spec: v1.LocalVolumeDeviceLinkSpec{
					PersistentVolumeName: "local-pv-existing",
					Policy:               v1.DeviceLinkPolicyNone,
				},
			},
			ownerObj: &v1.LocalVolume{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1.LocalVolumeKind,
					APIVersion: v1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lv-existing",
					Namespace: "default",
					UID:       types.UID("bbbbbbbb-2222-3333-4444-555555555555"),
				},
			},
			expectedPolicy:  v1.DeviceLinkPolicyNone,
			expectedListLen: 1,
		},
		{
			name:      "updates stale persistent volume name and preserves policy",
			pvName:    "local-pv-new",
			namespace: "default",
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local-pv-new",
					Namespace: "default",
				},
				Spec: v1.LocalVolumeDeviceLinkSpec{
					PersistentVolumeName: "local-pv-old",
					Policy:               v1.DeviceLinkPolicyCurrentLinkTarget,
				},
			},
			ownerObj: &v1.LocalVolume{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1.LocalVolumeKind,
					APIVersion: v1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lv-new",
					Namespace: "default",
					UID:       types.UID("cccccccc-2222-3333-4444-555555555555"),
				},
			},
			expectedPolicy:  v1.DeviceLinkPolicyCurrentLinkTarget,
			expectedListLen: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var runtimeObjects []runtime.Object
			if tc.existing != nil {
				runtimeObjects = append(runtimeObjects, tc.existing.DeepCopy())
			}

			fakeClient := newFakeDeviceLinkClient(t, runtimeObjects...).Build()
			handler := NewDeviceLinkHandler(tc.currentSymlink, tc.preferredSymlink, fakeClient)

			lvdl, err := handler.Create(t.Context(), tc.pvName, tc.namespace, tc.ownerObj)
			if err != nil {
				t.Fatalf("Create returned unexpected error: %v", err)
			}
			if lvdl == nil {
				t.Fatal("Create returned nil LVDL")
			}

			assert.Equal(t, tc.pvName, lvdl.Name)
			assert.Equal(t, tc.namespace, lvdl.Namespace)
			assert.Equal(t, tc.pvName, lvdl.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedPolicy, lvdl.Spec.Policy)
			if tc.existing == nil {
				assert.Len(t, lvdl.OwnerReferences, 1)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().Kind, lvdl.OwnerReferences[0].Kind)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().GroupVersion().String(), lvdl.OwnerReferences[0].APIVersion)
			}

			fetched := &v1.LocalVolumeDeviceLink{}
			if err := fakeClient.Get(context.TODO(), types.NamespacedName{Name: tc.pvName, Namespace: tc.namespace}, fetched); err != nil {
				t.Fatalf("Get after Create failed: %v", err)
			}
			assert.Equal(t, tc.pvName, fetched.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedPolicy, fetched.Spec.Policy)
			if tc.existing == nil {
				assert.Len(t, fetched.OwnerReferences, 1)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().Kind, fetched.OwnerReferences[0].Kind)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().GroupVersion().String(), fetched.OwnerReferences[0].APIVersion)
			}

			list := &v1.LocalVolumeDeviceLinkList{}
			if err := fakeClient.List(context.TODO(), list); err != nil {
				t.Fatalf("List failed: %v", err)
			}
			assert.Len(t, list.Items, tc.expectedListLen)
		})
	}
}

func TestDeviceLinkHandler_UpdateStatusAndPV(t *testing.T) {
	testCases := []struct {
		name             string
		pvName           string
		namespace        string
		currentSymlink   string
		preferredSymlink string
		kname            string
		devPath          string
		existing         *v1.LocalVolumeDeviceLink
		existingPV       *corev1.PersistentVolume
		preCreate        bool
		globLinks        []string
		filesystemUUID   string
		expectedLVDL     *v1.LocalVolumeDeviceLink
	}{
		{
			name:             "populates status with symlink targets and filesystem uuid",
			pvName:           "local-pv-statustest",
			namespace:        "default",
			currentSymlink:   "/dev/disk/by-id/wwn-current",
			preferredSymlink: "/dev/disk/by-id/wwn-preferred",
			kname:            "sda",
			devPath:          "/dev/sda",
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-statustest"},
			},
			existingPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest"},
			},
			globLinks:      []string{"/tmp/wwn-0x1234", "/tmp/scsi-abcde"},
			filesystemUUID: "550e8400-e29b-41d4-a716-446655440000",
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-statustest"},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:   "/dev/disk/by-id/wwn-current",
					PreferredLinkTarget: "/dev/disk/by-id/wwn-preferred",
					FilesystemUUID:      "550e8400-e29b-41d4-a716-446655440000",
					ValidLinkTargets:    []string{"/tmp/wwn-0x1234", "/tmp/scsi-abcde"},
				},
			},
		},
		{
			name:             "handles no by-id links and no filesystem uuid",
			pvName:           "local-pv-nolinks",
			namespace:        "default",
			currentSymlink:   "/dev/sdb",
			preferredSymlink: "",
			kname:            "sdb",
			devPath:          "/dev/sdb",
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-nolinks", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-nolinks"},
			},
			existingPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-nolinks"},
			},
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-nolinks", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-nolinks"},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:   "/dev/sdb",
					PreferredLinkTarget: "",
					FilesystemUUID:      "",
					ValidLinkTargets:    []string{},
				},
			},
		},
		{
			name:             "create then update full flow",
			pvName:           "local-pv-fullflow",
			namespace:        "openshift-local-storage",
			currentSymlink:   "/dev/disk/by-id/scsi-current",
			preferredSymlink: "/dev/disk/by-id/wwn-preferred",
			kname:            "sdc",
			devPath:          "/dev/sdc",
			preCreate:        true,
			existingPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-fullflow"},
			},
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-fullflow", Namespace: "openshift-local-storage"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-fullflow"},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:   "/dev/disk/by-id/scsi-current",
					PreferredLinkTarget: "/dev/disk/by-id/wwn-preferred",
					FilesystemUUID:      "",
					ValidLinkTargets:    []string{},
				},
			},
		},
		{
			name:             "returns without updating when pv does not exist",
			pvName:           "local-pv-missing",
			namespace:        "default",
			currentSymlink:   "/dev/sdd",
			preferredSymlink: "",
			kname:            "sdd",
			devPath:          "/dev/sdd",
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-missing", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-missing"},
			},
		},
		{
			name:             "returns without updating when lvdl does not exist",
			pvName:           "local-pv-lvdl-missing",
			namespace:        "default",
			currentSymlink:   "/dev/sde",
			preferredSymlink: "",
			kname:            "sde",
			devPath:          "/dev/sde",
			existingPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-lvdl-missing"},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var runtimeObjects []runtime.Object
			if tc.existing != nil {
				runtimeObjects = append(runtimeObjects, tc.existing.DeepCopy())
			}
			if tc.existingPV != nil {
				runtimeObjects = append(runtimeObjects, tc.existingPV.DeepCopy())
			}

			fakeClient := newFakeDeviceLinkClient(t, runtimeObjects...).Build()
			handler := NewDeviceLinkHandler(tc.currentSymlink, tc.preferredSymlink, fakeClient)

			if tc.preCreate {
				lvdl, err := handler.Create(context.TODO(), tc.pvName, tc.namespace, &v1.LocalVolume{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1.LocalVolumeKind,
						APIVersion: v1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "owner-precreate",
						Namespace: tc.namespace,
						UID:       types.UID("dddddddd-2222-3333-4444-555555555555"),
					},
				})
				if err != nil {
					t.Fatalf("Create failed: %v", err)
				}
				assert.Equal(t, tc.pvName, lvdl.Name)
				assert.Equal(t, tc.pvName, lvdl.Spec.PersistentVolumeName)
			}

			origGlob := FilePathGlob
			origEval := FilePathEvalSymLinks
			origExec := ExecCommand
			t.Cleanup(func() {
				FilePathGlob = origGlob
				FilePathEvalSymLinks = origEval
				ExecCommand = origExec
			})

			FilePathGlob = func(pattern string) ([]string, error) {
				return tc.globLinks, nil
			}
			FilePathEvalSymLinks = func(path string) (string, error) {
				return tc.devPath, nil
			}
			if tc.filesystemUUID == "" {
				ExecCommand = helperCommandBlkidEmpty()
			} else {
				ExecCommand = helperCommandBlkid(tc.filesystemUUID)
			}

			updated, err := handler.UpdateStatus(context.TODO(), tc.pvName, tc.namespace, tc.kname, tc.devPath)
			if err != nil {
				t.Fatalf("UpdateStatusAndPV returned unexpected error: %v", err)
			}
			if tc.expectedLVDL == nil {
				assert.Nil(t, updated)
				return
			}

			assert.Equal(t, tc.expectedLVDL.Name, updated.Name)
			assert.Equal(t, tc.expectedLVDL.Namespace, updated.Namespace)
			assert.Equal(t, tc.expectedLVDL.Spec.PersistentVolumeName, updated.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedLVDL.Status.CurrentLinkTarget, updated.Status.CurrentLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.PreferredLinkTarget, updated.Status.PreferredLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.FilesystemUUID, updated.Status.FilesystemUUID)
			assert.ElementsMatch(t, tc.expectedLVDL.Status.ValidLinkTargets, updated.Status.ValidLinkTargets)

			fetched := &v1.LocalVolumeDeviceLink{}
			if err := fakeClient.Get(context.TODO(), types.NamespacedName{Name: tc.pvName, Namespace: tc.namespace}, fetched); err != nil {
				t.Fatalf("Get after UpdateStatusAndPV failed: %v", err)
			}
			assert.Equal(t, tc.expectedLVDL.Name, fetched.Name)
			assert.Equal(t, tc.expectedLVDL.Namespace, fetched.Namespace)
			assert.Equal(t, tc.expectedLVDL.Spec.PersistentVolumeName, fetched.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedLVDL.Status.CurrentLinkTarget, fetched.Status.CurrentLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.PreferredLinkTarget, fetched.Status.PreferredLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.FilesystemUUID, fetched.Status.FilesystemUUID)
			assert.ElementsMatch(t, tc.expectedLVDL.Status.ValidLinkTargets, fetched.Status.ValidLinkTargets)
		})
	}
}

func TestDeviceLinkHandler_CreateFailsWithNilOwnerObject(t *testing.T) {
	fakeClient := newFakeDeviceLinkClient(t).Build()
	handler := NewDeviceLinkHandler("/dev/disk/by-id/current", "/dev/disk/by-id/preferred", fakeClient)

	lvdl, err := handler.Create(t.Context(), "local-pv-nil-owner", "default", nil)
	assert.Nil(t, lvdl)
	assert.Error(t, err)
}
