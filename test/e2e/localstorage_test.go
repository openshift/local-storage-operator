package e2e

import (
	goctx "context"
	"testing"
	"time"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

var (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 120
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func addToScheme(s *runtime.Scheme) error {
	return localv1.AddToScheme(s)
}

func TestLocalStorageOperator(t *testing.T) {
	localVolumeList := &localv1.LocalVolumeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.SchemeGroupVersion.String(),
		},
	}
	err := framework.AddToFrameworkScheme(addToScheme, localVolumeList)
	if err != nil {
		t.Fatalf("error adding local volume list : %v", err)
	}

	ctx := framework.NewTestCtx(t)
	defer ctx.Cleanup()

	err = waitForOperatorToBeReady(t, ctx)
	if err != nil {
		t.Fatalf("error waiting for operator to be ready : %v", err)
	}

	f := framework.Global
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("error fetching namespace : %v", err)
	}

	localVolume := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-local-disk",
			Namespace: namespace,
		},
		Spec: localv1.LocalVolumeSpec{
			StorageClassDevices: []localv1.StorageClassDevice{
				{
					StorageClassName: "test-local-sc",
					DevicePaths:      []string{"/dev/foobar"},
				},
			},
		},
	}

	err = f.Client.Create(goctx.TODO(), localVolume, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("error creating localvolume cr : %v", err)
	}

	provisionerDSName := localVolume.Name + "-local-provisioner"
	diskMakerDSName := localVolume.Name + "-local-diskmaker"

	err = waitForDaemonSet(t, f.KubeClient, namespace, provisionerDSName, retryInterval, timeout)
	if err != nil {
		t.Fatalf("error waiting for provisioner daemonset : %v", err)
	}

	err = waitForDaemonSet(t, f.KubeClient, namespace, diskMakerDSName, retryInterval, timeout)
	if err != nil {
		t.Fatalf("error waiting for diskmaker daemonset : %v", err)
	}
}

func waitForOperatorToBeReady(t *testing.T, ctx *framework.TestCtx) error {
	t.Log("Initializing cluster resources...")
	err := ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}
	t.Log("Initialized cluster resources")
	namespace, err := ctx.GetNamespace()
	if err != nil {
		return err
	}
	t.Logf("Found namespace: %v", namespace)

	// get global framework variables
	f := framework.Global
	// wait for local-storage-operator to be ready
	t.Log("Waiting for local-storage-operator to be ready...")
	err = e2eutil.WaitForDeployment(t, f.KubeClient, namespace, "local-storage-operator", 1, retryInterval, timeout)
	if err != nil {
		return err
	}
	return nil
}

func waitForDaemonSet(t *testing.T, kubeclient kubernetes.Interface, namespace, name string, retryInterval, timeout time.Duration) error {
	nodes, err := kubeclient.Core().Nodes().List(metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker"})
	if err != nil {
		return err
	}
	nodeCount := len(nodes.Items)
	err = wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(name, metav1.GetOptions{IncludeUninitialized: true})
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s daemonset\n", name)
				return false, nil
			}
			return false, err
		}
		if int(daemonset.Status.NumberReady) == nodeCount {
			return true, nil
		}
		t.Logf("Waiting for full availability of %s daemonset (%d/%d)\n", name, int(daemonset.Status.NumberReady), nodeCount)
		return false, nil
	})
	if err != nil {
		return err
	}
	t.Logf("Daemonset available (%d/%d)\n", nodeCount, nodeCount)
	return nil
}
