package deleter

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/openshift/client-go/security/clientset/versioned/scheme"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	v1api "github.com/openshift/local-storage-operator/api/v1"
	v1alphav1api "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/test-framework"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crFake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

//go:embed *
var f embed.FS

const (
	storageClassName = "local-sc"
)

type testContext struct {
	fakeClient     client.WithWatch
	fakeRecorder   *record.FakeRecorder
	eventStream    chan string
	fakeMounter    *mount.FakeMounter
	fakeVolUtil    *provUtil.FakeVolumeUtil
	fakeDirFiles   map[string][]*provUtil.FakeDirEntry
	runtimeConfig  *provCommon.RuntimeConfig
	cleanupTracker *provDeleter.CleanupStatusTracker
}

type NodeConfigParams struct {
	NodeName string
	NodeUID  types.UID
}

func makeNodeList(params []NodeConfigParams) *corev1.NodeList {
	mockNodeList := corev1.NodeList{
		TypeMeta: metav1.TypeMeta{
			Kind: "NodeList",
		},
		Items: []corev1.Node{},
	}

	for _, p := range params {
		node := corev1.Node{
			TypeMeta: metav1.TypeMeta{
				Kind: "Node",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: p.NodeName,
				UID:  p.NodeUID,
			},
		}
		mockNodeList.Items = append(mockNodeList.Items, node)
	}

	return &mockNodeList
}

type PVConfigParams struct {
	PVName          string
	PVAnnotation    string
	PVReclaimPolicy corev1.PersistentVolumeReclaimPolicy
	PVPhase         corev1.PersistentVolumePhase
}

func makePersistentVolumeList(params []PVConfigParams) *corev1.PersistentVolumeList {

	mockPersistentVolumeList := corev1.PersistentVolumeList{
		TypeMeta: metav1.TypeMeta{
			Kind: "PersistentVolumeList",
		},
		Items: nil,
	}

	if params == nil {
		return &mockPersistentVolumeList
	}

	for _, p := range params {
		pv := corev1.PersistentVolume{
			TypeMeta: metav1.TypeMeta{
				Kind: "PersistentVolume",
			},
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					provCommon.AnnProvisionedBy: p.PVAnnotation,
				},
				Name: p.PVName,
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeReclaimPolicy: p.PVReclaimPolicy,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					Local: &corev1.LocalVolumeSource{
						Path: filepath.Join("/mnt/local-storage", storageClassName, p.PVName),
					},
				},
				StorageClassName: storageClassName, //Has to match with ConfigMap (data.storageClassMap)
			},
			Status: corev1.PersistentVolumeStatus{
				Phase: p.PVPhase,
			},
		}
		mockPersistentVolumeList.Items = append(mockPersistentVolumeList.Items, pv)
	}

	return &mockPersistentVolumeList
}

func newFakeDeleteReconciler(t *testing.T, objs ...runtime.Object) (*DeleteReconciler, *testContext) {
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

	fakeRecorder := record.NewFakeRecorder(20)
	fakeClient := crFake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	fakeVolUtil := provUtil.NewFakeVolumeUtil(false /*deleteShouldFail*/, map[string][]*provUtil.FakeDirEntry{})
	mounter := &mount.FakeMounter{
		MountPoints: []mount.MountPoint{},
	}
	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node: &corev1.Node{},
		},
		Cache:    provCache.NewVolumeCache(),
		VolUtil:  fakeVolUtil,
		APIUtil:  test.ApiUtil{Client: fakeClient},
		Recorder: fakeRecorder,
		Mounter:  mounter,
	}

	cleanupTracker := &provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()}
	deleteReconciler := NewDeleteReconciler(
		fakeClient,
		&provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()},
		runtimeConfig,
	)

	tc := &testContext{
		fakeClient:     fakeClient,
		fakeRecorder:   fakeRecorder,
		eventStream:    fakeRecorder.Events,
		fakeMounter:    mounter,
		runtimeConfig:  runtimeConfig,
		fakeVolUtil:    fakeVolUtil,
		cleanupTracker: cleanupTracker,
	}

	return deleteReconciler, tc
}

func TestDeleterReconcile(t *testing.T) {
	tests := []struct {
		name           string
		dirEntries     []*provUtil.FakeDirEntry
		nodeList       *corev1.NodeList
		targetNodeName string
		initialPVs     *corev1.PersistentVolumeList
		expectedPVs    *corev1.PersistentVolumeList
	}{
		{
			name:       "Reconcile does not delete a PV (Reclaim policy: Retain)",
			dirEntries: nil,
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			// Name of a node that deleter Reconcile() will see, normally it would get it from env var - it's the name of the node the code runs on.
			targetNodeName: "Node1",
			initialPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
			expectedPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
		},
		{
			name: "Reconcile deletes only PVs that belong to its node (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
				{
					Name:       "PV-2",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
				{
					PVName:          "PV-2",
					PVAnnotation:    "local-volume-provisioner-Node2-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
			expectedPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-2",
					PVAnnotation:    "local-volume-provisioner-Node2-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
		},
		{
			name: "Reconcile deletes a PV with UID (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
			expectedPVs: &corev1.PersistentVolumeList{},
		},
		{
			name: "Reconcile deletes a PV without UID (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
			expectedPVs: &corev1.PersistentVolumeList{},
		},
		{
			name: "Reconcile deletes a PV if node UID does not match PV annotation (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "5991475c-f876-11ec-b939-0242ac120002",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: makePersistentVolumeList([]PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			}),
			expectedPVs: &corev1.PersistentVolumeList{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := []runtime.Object{
				test.nodeList,
				test.initialPVs,
			}

			cmBytes, err := f.ReadFile("testfiles/provisioner_conf.yaml")
			if err != nil {
				t.Fatalf("Failed to load config map: %v", err)
			}
			configMap := resourceread.ReadConfigMapV1OrDie(cmBytes)

			r, tc := newFakeDeleteReconciler(t, objects...)

			err = tc.fakeClient.Create(context.TODO(), configMap)
			if err != nil {
				t.Fatalf("Failed to create ConfigMap: %v", err)
			}

			// Rewrite nodeName which is set in init() of the module (from env var).
			nodeName = test.targetNodeName

			cmNamespace := configMap.GetObjectMeta().GetNamespace()
			cmName := configMap.GetObjectMeta().GetName()
			reconRequest := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: cmName, Namespace: cmNamespace},
			}

			// Create fake directory entries.
			dirFiles := map[string][]*provUtil.FakeDirEntry{
				storageClassName: test.dirEntries,
			}
			tc.fakeVolUtil.AddNewDirEntries("/mnt/local-storage", dirFiles)

			// Run Reconcile() attempts evaluating state of PVs in each iteration.
			retries := 5
			retryWait := 1 * time.Second
			checkPassed := false
			for i := retries; i >= 0; i-- {
				if !checkPassed {
					result, err := r.Reconcile(context.TODO(), reconRequest)
					if err != nil {
						t.Fatalf("Reconcile failed: %v", err)
					}
					if result.Requeue {
						time.Sleep(result.RequeueAfter)
						result, err = r.Reconcile(context.TODO(), reconRequest)
					}
					ok, msg := evaluate(tc, test.expectedPVs)
					if !ok {
						t.Logf("Reconcile evaluation did not pass with message: %v attempts remaining: %v", msg, i)
						t.Logf("Retry after %v", retryWait)
						time.Sleep(retryWait)
						continue
					} else {
						checkPassed = true
						t.Log("Reconcile check passed!")
					}

				}
			}
			if !checkPassed {
				t.Fatalf("Reconcile check failed!")
			}
		})
	}
}

func evaluate(tc *testContext, expectedPVs *corev1.PersistentVolumeList) (bool, string) {
	// Get PVs after reconcile.
	currentPVs := &corev1.PersistentVolumeList{}
	err := tc.fakeClient.List(context.TODO(), currentPVs)
	if err != nil {
		msg := fmt.Sprintf("Failed to get PVs: %v", err)
		return false, msg
	}

	var actualPVsValues []corev1.PersistentVolume
	actualPVsValues = currentPVs.Items

	actualPVsValuesCopy := make([]corev1.PersistentVolume, len(actualPVsValues))
	copy(actualPVsValuesCopy, actualPVsValues)

	actualPVsValuesCopy2 := make([]corev1.PersistentVolume, len(actualPVsValues))
	copy(actualPVsValuesCopy2, actualPVsValues)

	// Test that there are no extra PVs left apart from expected ones.
	for a, actualPV := range actualPVsValuesCopy {
		for _, expectedPV := range expectedPVs.Items {
			actualPV.SetResourceVersion("")
			expectedPV.SetResourceVersion("")
			if reflect.DeepEqual(actualPV, expectedPV) {
				actualPVsValuesCopy = RemoveIndex(actualPVsValuesCopy, a)
			}

		}
	}

	if len(expectedPVs.Items) == 0 && len(actualPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nExpected to find no PVs after reconcile but some were still found: %v", actualPVsValuesCopy)
		return false, msg
	}

	if len(actualPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nFound PVs that were not expected!\nThese PVs are actually present but should not be:\n%v", actualPVsValuesCopy)
		return false, msg
	}

	// Test that every expected PV is found.
	expectedPVsValuesCopy := make([]corev1.PersistentVolume, len(expectedPVs.Items))
	copy(expectedPVsValuesCopy, expectedPVs.Items)

	if len(expectedPVs.Items) != 0 {
		for _, actualPV := range actualPVsValuesCopy2 {
			for e, expectedPV := range expectedPVsValuesCopy {
				actualPV.SetResourceVersion("")
				expectedPV.SetResourceVersion("")
				if reflect.DeepEqual(actualPV, expectedPV) {
					expectedPVsValuesCopy = RemoveIndex(expectedPVsValuesCopy, e)
				}
			}
		}
	}

	if len(expectedPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nNot all expected PVs found!\nThese PVs were expected but not found:\n%v", expectedPVsValuesCopy)
		return false, msg
	}

	return true, ""
}

func RemoveIndex(s []corev1.PersistentVolume, index int) []corev1.PersistentVolume {
	ret := make([]corev1.PersistentVolume, 0)
	ret = append(ret, s[:index]...)
	return append(ret, s[index+1:]...)
}
