package lv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

// TestHelperProcess is the subprocess used by helperCommandLVBlkid to fake blkid
// output without forking a real binary. It is never called directly by the test
// runner; the guard at the top of the function prevents that.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)
	if os.Getenv("COMMAND") == "blkid" {
		fmt.Fprintf(os.Stdout, "%s", os.Getenv("BLKIDOUT"))
	}
}

// helperCommandLVBlkid returns a fake ExecCommand that makes blkid emit uuid,
// but only when it is invoked with devicePath as its last argument.  When any
// other path is passed the output is empty, which is what the real blkid
// returns when it cannot find a UUID.  This asymmetry is what makes the test
// sensitive to argument-order bugs in the UpdateStatusAndPV call.
func helperCommandLVBlkid(devicePath, uuid string) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		// Only emit the UUID when the last argument matches devicePath.
		out := ""
		if command == "blkid" && len(args) > 0 && args[len(args)-1] == devicePath {
			out = uuid
		}
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("COMMAND=%s", command),
			fmt.Sprintf("BLKIDOUT=%s", out),
			fmt.Sprintf("GOCOVERDIR=%s", os.TempDir()),
		}
		return cmd
	}
}

func TestCreatePV(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	testTable := []struct {
		desc      string
		shouldErr bool
		lv        localv1.LocalVolume
		node      corev1.Node
		sc        storagev1.StorageClass
		// device stuff
		symlinkpath     string
		actualVolMode   string
		desiredVolMode  string
		deviceName      string
		deviceCapacity  int64
		mountPoints     sets.String
		extraDirEntries []*provUtil.FakeDirEntry
	}{
		{
			desc: "basic creation: block on block",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeBlock),
			desiredVolMode: string(localv1.PersistentVolumeBlock),
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "basic creation: block on fs",
			shouldErr: true,
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeBlock),
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs on block",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeBlock),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.NewString("/mnt/local-storage/storageclass-a/device-a"),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "actual volume mode is fs, but is not mountpoint",
			shouldErr: true,
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-b",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-b",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-b"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-b",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.NewString("a", "b"), // device not present
			symlinkpath:    "/mnt/local-storage/storageclass-b/device-b",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-b",
		},
	}
	// iterate through testcases
	for i, tc := range testTable {
		t.Logf("Test Case #%d: %q", i, tc.desc)

		// fake setup
		if tc.lv.Namespace == "" {
			tc.lv.Namespace = "default"
		}
		tc.lv.Kind = localv1.LocalVolumeKind
		r, testConfig := getFakeDiskMaker(t, "/mnt/local-storage", &tc.lv, &tc.node, &tc.sc)
		testConfig.runtimeConfig.Node = &tc.node
		testConfig.runtimeConfig.Name = common.GetProvisionedByValue(tc.node)
		testConfig.runtimeConfig.DiscoveryMap[tc.sc.Name] = provCommon.MountConfig{VolumeMode: tc.desiredVolMode}

		fakeMap := map[string]string{
			string(corev1.PersistentVolumeFilesystem): provUtil.FakeEntryFile,
			string(corev1.PersistentVolumeBlock):      provUtil.FakeEntryBlock,
		}
		if len(tc.extraDirEntries) == 0 {
			tc.extraDirEntries = make([]*provUtil.FakeDirEntry, 0)
		}

		tc.extraDirEntries = append(tc.extraDirEntries, &provUtil.FakeDirEntry{
			Name:       tc.deviceName,
			Capacity:   tc.deviceCapacity,
			VolumeType: fakeMap[tc.actualVolMode],
		})
		dirFiles := map[string][]*provUtil.FakeDirEntry{
			tc.sc.Name: tc.extraDirEntries,
		}
		testConfig.fakeVolUtil.AddNewDirEntries("/mnt/local-storage/", dirFiles)

		err := common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
			LocalVolumeLikeObject: &tc.lv,
			RuntimeConfig:         r.runtimeConfig,
			StorageClass:          tc.sc,
			MountPointMap:         tc.mountPoints,
			Client:                r.Client,
			SymLinkPath:           tc.symlinkpath,
			BlockDevice:           internal.BlockDevice{KName: filepath.Base(tc.deviceName)},
			IDExists:              true,
			ExtraLabelsForPV:      map[string]string{},
		})
		if tc.shouldErr {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}

		if tc.shouldErr {
			return
		}
		pv := &corev1.PersistentVolume{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: common.GeneratePVName(filepath.Base(tc.symlinkpath), tc.node.GetName(), tc.sc.GetName())}, pv)

		// provisioned-by annotation accurate
		actualProvName, found := pv.ObjectMeta.Annotations[provCommon.AnnProvisionedBy]
		assert.True(t, found)
		assert.Equal(t, testConfig.runtimeConfig.Name, actualProvName)

		// capacity accurate
		pvCapacity, found := pv.Spec.Capacity["storage"]
		assert.True(t, found)
		expectedCapacity := resource.MustParse(fmt.Sprint(common.RoundDownCapacityPretty(tc.deviceCapacity)))

		assert.Truef(t, pvCapacity.Equal(expectedCapacity), "actual: %s,expected: %s", pvCapacity, expectedCapacity)

		// pvName accurate
		assert.Equal(t, common.GeneratePVName(filepath.Base(tc.symlinkpath), tc.node.Name, tc.sc.Name), pv.Name)

		// symlinkPath accurate
		assert.NotNil(t, pv.Spec.Local)
		assert.Equal(t, tc.symlinkpath, pv.Spec.Local.Path)

		// storageclass accurate
		assert.Equal(t, tc.sc.Name, pv.Spec.StorageClassName)

		// reclaimPolicy accurate,
		assert.Equal(t, *tc.sc.ReclaimPolicy, pv.Spec.PersistentVolumeReclaimPolicy)

		// test idempotency by running again
		err = common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
			LocalVolumeLikeObject: &tc.lv,
			RuntimeConfig:         r.runtimeConfig,
			StorageClass:          tc.sc,
			MountPointMap:         tc.mountPoints,
			Client:                r.Client,
			SymLinkPath:           tc.symlinkpath,
			BlockDevice:           internal.BlockDevice{KName: filepath.Base(tc.deviceName)},
			IDExists:              true,
			ExtraLabelsForPV:      map[string]string{},
		})
		assert.Nil(t, err)

	}

}

// TestCreateLocalPV_DeviceLinkArgOrder verifies that CreateLocalPV passes KName
// to the by-id symlink matcher and DevicePath to blkid — and not the other way
// around.  A previous bug had these two arguments swapped in the
// ApplyStatus call inside CreateLocalPV.
//
// The test uses deliberately distinct values for KName and DevicePath so that
// swapping them would produce wrong results:
//
//   - FilePathGlob / FilePathEvalSymLinks are mocked to recognise only KName
//     when resolving by-id links → ValidLinkTargets must be non-empty.
//   - ExecCommand (blkid) is mocked to return a UUID only when its last
//     argument equals DevicePath → FilesystemUUID must equal fakeUUID.
//
// If the arguments were swapped:
//   - blkid would receive KName ("sda") instead of DevicePath ("/dev/sda")
//     and would produce an empty UUID, causing the FilesystemUUID assertion
//     to fail.
//   - The symlink matcher would receive DevicePath and filepath.Base of that
//     would still be "sda", so to make the test more robust the mock checks
//     the exact string passed to FilePathEvalSymLinks as its target.
func TestCreateLocalPV_DeviceLinkArgOrder(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	kName := "sda"
	devPath := "/dev/" + kName // deliberately different from kName
	fakeUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	fakeByIDLink := "/dev/disk/by-id/wwn-0x" + kName

	lv := localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{Kind: localv1.LocalVolumeKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lv-argorder",
			Namespace: "openshift-local-storage",
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-argorder",
			Labels: map[string]string{corev1.LabelHostname: "node-hostname-argorder"},
		},
	}
	sc := storagev1.StorageClass{
		ObjectMeta:    metav1.ObjectMeta{Name: "sc-argorder"},
		ReclaimPolicy: &reclaimPolicyDelete,
	}

	r, testConfig := getFakeDiskMaker(t, "/mnt/local-storage", &lv, &node, &sc)
	testConfig.runtimeConfig.Node = &node
	testConfig.runtimeConfig.Name = common.GetProvisionedByValue(node)
	testConfig.runtimeConfig.Namespace = lv.Namespace
	testConfig.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
		VolumeMode: string(localv1.PersistentVolumeBlock),
	}

	symLinkPath := "/mnt/local-storage/" + sc.Name + "/" + kName
	testConfig.fakeVolUtil.AddNewDirEntries("/mnt/local-storage/", map[string][]*provUtil.FakeDirEntry{
		sc.Name: {
			{Name: kName, Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
		},
	})

	// Override package-level globals for the duration of this test.
	origGlob := internal.FilePathGlob
	origEval := internal.FilePathEvalSymLinks
	origExec := internal.ExecCommand
	t.Cleanup(func() {
		internal.FilePathGlob = origGlob
		internal.FilePathEvalSymLinks = origEval
		internal.ExecCommand = origExec
	})

	// FilePathGlob returns our fake by-id link.
	internal.FilePathGlob = func(pattern string) ([]string, error) {
		return []string{fakeByIDLink}, nil
	}

	// FilePathEvalSymLinks resolves the by-id link to a path whose base is
	// kName ("sda").  If KName and DevicePath were swapped the matcher would
	// call this with a path that still resolves correctly (because
	// filepath.Base("/dev/sda") == "sda"), so we record which target was
	// actually resolved to make the test stricter.
	var resolvedTarget string
	internal.FilePathEvalSymLinks = func(path string) (string, error) {
		resolvedTarget = path
		// Return a path whose base is kName so the symlink appears valid.
		return "/dev/" + kName, nil
	}

	// blkid returns fakeUUID only when called with devPath.
	// If the caller passes kName instead, the UUID will be empty.
	internal.ExecCommand = helperCommandLVBlkid(devPath, fakeUUID)

	err := common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
		LocalVolumeLikeObject: &lv,
		RuntimeConfig:         r.runtimeConfig,
		StorageClass:          sc,
		MountPointMap:         sets.NewString(),
		Client:                r.Client,
		SymLinkPath:           symLinkPath,
		BlockDevice:           internal.BlockDevice{KName: kName, PathByID: fakeByIDLink},
		IDExists:              true,
		ExtraLabelsForPV:      map[string]string{},
	})
	assert.NoError(t, err)

	// Fetch the LocalVolumeDeviceLink that CreateLocalPV must have created.
	pvName := common.GeneratePVName(filepath.Base(symLinkPath), node.Name, sc.Name)
	lvdl := &localv1.LocalVolumeDeviceLink{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: lv.Namespace}, lvdl)
	assert.NoError(t, err, "LVDL should have been created by CreateLocalPV")
	if assert.Len(t, lvdl.OwnerReferences, 1, "LVDL should carry owner reference to LocalVolume") {
		assert.Equal(t, localv1.GroupVersion.String(), lvdl.OwnerReferences[0].APIVersion)
		assert.Equal(t, localv1.LocalVolumeKind, lvdl.OwnerReferences[0].Kind)
		assert.Equal(t, lv.Name, lvdl.OwnerReferences[0].Name)
	}

	// ValidLinkTargets must be populated — proving KName was used for the
	// by-id glob matching (not DevicePath).
	assert.NotEmpty(t, lvdl.Status.ValidLinkTargets,
		"ValidLinkTargets should be populated; KName must have been passed to the symlink matcher")
	assert.Contains(t, lvdl.Status.ValidLinkTargets, fakeByIDLink)

	// FilesystemUUID must equal fakeUUID — proving DevicePath was passed to
	// blkid (not KName).  If the args were swapped, blkid would receive "sda"
	// instead of "/dev/sda" and would produce an empty string.
	assert.Equal(t, fakeUUID, lvdl.Status.FilesystemUUID,
		"FilesystemUUID should match fakeUUID; DevicePath must have been passed to blkid")

	// Sanity check: the symlink evaluator was called with the by-id link,
	// confirming the glob result was actually used.
	assert.Equal(t, fakeByIDLink, resolvedTarget,
		"FilePathEvalSymLinks should have been called with the by-id link")
}
