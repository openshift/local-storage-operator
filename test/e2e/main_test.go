package e2e

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	localapisv1 "github.com/openshift/local-storage-operator/pkg/apis"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"github.com/stretchr/testify/assert"
)

var (
	retryInterval = time.Second * 5
	// timeout              = time.Second * 120
	timeout              = time.Hour
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5

	// testmap is a map from test-name to test-wrapper.
	// test-wrapper returns a func that can be run with t.Run(func(*testing.T), but
	// has access to a framework.Context and cleanupFuncs via closure.
	testMap = map[string]func(*framework.Context, *[]cleanupFn) func(*testing.T){
		// "LocalVolumeDiscovery": LocalVolumeDiscoveryTest,
		"LocalVolume": LocalVolumeTest,
		// "LocalVolumeSet":       LocalVolumeSetTest,
	}
)

func TestMain(m *testing.M) {
	framework.MainEntry(m)
}

// TestLocalStorage runs the tests and handles the setup and teardown of the test environment.
// Each test is a closure func(*testing.T) that has access to a context.
// Additional setup can be done within the test
func TestLocalStorage(t *testing.T) {

	// register CRD schemes
	localVolumeDiscoveryList := &localv1alpha1.LocalVolumeDiscoveryList{}
	err := framework.AddToFrameworkScheme(localapisv1.AddToScheme, localVolumeDiscoveryList)
	if err != nil {
		t.Fatalf("error adding local volume discovery list : %v", err)
	}

	localVolumeList := &localv1.LocalVolumeList{}
	err = framework.AddToFrameworkScheme(localapisv1.AddToScheme, localVolumeList)
	if err != nil {
		t.Fatalf("error adding local volume list : %v", err)
	}
	localVolumeSetList := &localv1alpha1.LocalVolumeSetList{}
	err = framework.AddToFrameworkScheme(localapisv1.AddToScheme, localVolumeSetList)
	if err != nil {
		t.Fatalf("error adding local volume set list : %v", err)
	}

	// Run tests with setup and teardown
	for testName, testWrapper := range testMap {
		context := framework.NewTestCtx(t)

		// a list of functions that will be run at the end of every test suite
		// they will be run even if an interrupt is sent
		// test suites can register functions
		cleanupFuncs := make([]cleanupFn, 0)
		runCleanup := getCleanupRunner(t, &cleanupFuncs)

		// handle ctrl-c
		stopChan := make(chan os.Signal)
		signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			// block until interrupt recieved
			<-stopChan
			fmt.Println("\r- Interrupt recieved, cleaning up")
			runCleanup()
			os.Exit(1)

		}()
		// add context cleanup to cleanup funcs
		addToCleanupFuncs(&cleanupFuncs, "cleanup test context", func(t *testing.T) error {
			context.Cleanup()
			return nil
		})

		// deploy the local-storage-operator
		err = waitForOperatorToBeReady(t, context)
		if err != nil {
			t.Fatalf("error waiting for operator to be ready : %v", err)
		}

		testWithContext := testWrapper(context, &cleanupFuncs)
		t.Run(testName, testWithContext)

		errs := runCleanup()
		for _, err := range errs {
			assert.NoErrorf(t, err, "expected cleanup step to succeed")
		}
	}

}

type cleanupFn struct {
	name string
	fn   func(t *testing.T) error
}

// return a threadsafe func()[]error that has access to t and cleanupFuncs as a closure, and runs only once
// runs cleanupFuncs in reverse order
func getCleanupRunner(t *testing.T, cleanupFuncs *[]cleanupFn) func() []error {
	mux := sync.Mutex{}
	started := false
	return func() []error {
		mux.Lock()
		// lock can only be reacquired after function returns
		defer mux.Unlock()
		if !started {
			started = true
			errs := make([]error, 0)
			funcs := *cleanupFuncs
			t.Logf("running %d cleanup functions.", len(funcs))
			// run in reverse
			for i := range funcs {
				f := funcs[len(funcs)-(i+1)]
				t.Logf("running %d/%d cleanup function: %s", i+1, len(funcs), f.name)
				err := f.fn(t)
				if err != nil {
					t.Logf("failed cleanup step: %+v", err)
				}
				errs = append(errs, err)
			}
			return errs
		}
		return []error{}
	}

}

func addToCleanupFuncs(cleanupFuncs *[]cleanupFn, name string, fn func(*testing.T) error) {
	*cleanupFuncs = append(*cleanupFuncs,
		cleanupFn{
			name: name,
			fn:   fn,
		},
	)
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

// env var config helpers

func getOperatorImage() string {
	return os.Getenv("IMAGE_LOCAL_DISKMAKER")
}

func getDiskMakerImage() string {
	return os.Getenv("IMAGE_LOCAL_DISKMAKER")
}
