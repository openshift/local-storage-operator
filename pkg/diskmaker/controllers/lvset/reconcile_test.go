package lvset

import (
	"context"
	"testing"

	"github.com/openshift/client-go/security/clientset/versioned/scheme"
	"github.com/openshift/local-storage-operator/pkg/apis"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/util/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crFake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	"sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
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

func newFakeLocalVolumeSetReconciler(t *testing.T, objs ...runtime.Object) (*ReconcileLocalVolumeSet, *testContext) {
	scheme := scheme.Scheme

	err := apis.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding storagev1 to scheme")

	fakeClient := crFake.NewFakeClientWithScheme(scheme, objs...)

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
		APIUtil:  apiUtil{client: fakeClient},
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

	cleanupTracker := &provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()}
	return &ReconcileLocalVolumeSet{
		client:         fakeClient,
		scheme:         scheme,
		eventReporter:  newEventReporter(fakeRecorder),
		deviceAgeMap:   newAgeMap(fakeClock),
		cleanupTracker: &provDeleter.CleanupStatusTracker{ProcTable: deleter.NewProcTable()},
		runtimeConfig:  runtimeConfig,
		deleter:        provDeleter.NewDeleter(runtimeConfig, cleanupTracker),
	}, tc
}

type apiUtil struct {
	client client.Client
}

// Create PersistentVolume object
func (a apiUtil) CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error) {
	return pv, a.client.Create(context.TODO(), pv)
}

// Delete PersistentVolume object
func (a apiUtil) DeletePV(pvName string) error {
	pv := &corev1.PersistentVolume{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: pvName}, pv)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.client.Delete(context.TODO(), pv)
	return err
}

// CreateJob Creates a Job execution.
func (a apiUtil) CreateJob(job *batchv1.Job) error {
	return a.client.Create(context.TODO(), job)
}

// DeleteJob deletes specified Job by its name and namespace.
func (a apiUtil) DeleteJob(jobName string, namespace string) error {
	job := &batchv1.Job{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: jobName}, job)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.client.Delete(context.TODO(), job)
	return err
}
