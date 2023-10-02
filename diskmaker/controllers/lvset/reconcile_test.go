package lvset

import (
	"testing"

	"github.com/openshift/local-storage-operator/test-framework"

	"github.com/openshift/client-go/security/clientset/versioned/scheme"
	v1api "github.com/openshift/local-storage-operator/api/v1"
	v1alphav1api "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/mount"

	"sigs.k8s.io/controller-runtime/pkg/client"
	crFake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const (
	testNamespace = "default"
)

// testConfig allows manipulating the fake objects for ReconcileLocalVolumeSet
type testContext struct {
	fakeClient    client.Client
	fakeRecorder  *record.FakeRecorder
	eventStream   chan string
	fakeClock     *fakeClock
	fakeMounter   *mount.FakeMounter
	fakeVolUtil   *provUtil.FakeVolumeUtil
	fakeDirFiles  map[string][]*provUtil.FakeDirEntry
	runtimeConfig *provCommon.RuntimeConfig
}

func newFakeLocalVolumeSetReconciler(t *testing.T, objs ...runtime.Object) (*LocalVolumeSetReconciler, *testContext) {
	scheme := scheme.Scheme

	err := v1api.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = v1alphav1api.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding storagev1 to scheme")

	fakeClient := crFake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

	fakeRecorder := record.NewFakeRecorder(20)
	eventChannel := fakeRecorder.Events
	fakeClock := &fakeClock{}
	mounter := &mount.FakeMounter{
		MountPoints: []mount.MountPoint{},
	}

	fakeVolUtil := provUtil.NewFakeVolumeUtil(false /*deleteShouldFail*/, map[string][]*provUtil.FakeDirEntry{})

	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node:         &corev1.Node{},
			DiscoveryMap: make(map[string]provCommon.MountConfig),
		},
		Cache:    provCache.NewVolumeCache(),
		VolUtil:  fakeVolUtil,
		APIUtil:  test.ApiUtil{Client: fakeClient},
		Recorder: fakeRecorder,
		Mounter:  mounter,
	}
	tc := &testContext{
		fakeClient:    fakeClient,
		fakeRecorder:  fakeRecorder,
		eventStream:   eventChannel,
		fakeClock:     fakeClock,
		fakeMounter:   mounter,
		runtimeConfig: runtimeConfig,
		fakeVolUtil:   fakeVolUtil,
	}

	lvsReconciler := NewLocalVolumeSetReconciler(
		fakeClient,
		scheme,
		fakeClock,
		&provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()},
		runtimeConfig,
	)

	return lvsReconciler, tc
}
