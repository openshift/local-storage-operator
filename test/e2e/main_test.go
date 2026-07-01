package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/local-storage-operator/api"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	framework "github.com/openshift/local-storage-operator/test/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMain(m *testing.M) {
	framework.MainEntry(m)
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Local Storage Operator E2E Suite")
}

var _ = BeforeSuite(func() {
	GinkgoWriter.TeeTo(os.Stdout)

	localVolumeDiscoveryList := &localv1alpha1.LocalVolumeDiscoveryList{}
	Expect(framework.AddToFrameworkScheme(api.AddToScheme, localVolumeDiscoveryList)).To(Succeed())
	Expect(framework.AddToFrameworkScheme(localv1.AddToScheme, localVolumeDiscoveryList)).To(Succeed())
	Expect(framework.AddToFrameworkScheme(localv1alpha1.AddToScheme, localVolumeDiscoveryList)).To(Succeed())

	localVolumeList := &localv1.LocalVolumeList{}
	Expect(framework.AddToFrameworkScheme(localv1.AddToScheme, localVolumeList)).To(Succeed())
	Expect(framework.AddToFrameworkScheme(localv1alpha1.AddToScheme, localVolumeList)).To(Succeed())

	localVolumeSetList := &localv1alpha1.LocalVolumeSetList{}
	Expect(framework.AddToFrameworkScheme(localv1.AddToScheme, localVolumeSetList)).To(Succeed())
	Expect(framework.AddToFrameworkScheme(localv1alpha1.AddToScheme, localVolumeSetList)).To(Succeed())

	f := framework.Global
	namespace := f.OperatorNamespace

	f.Logf("Waiting for local-storage-operator to be ready in namespace %s", namespace)
	Eventually(func(ctx context.Context) error {
		deployment, err := f.KubeClient.AppsV1().Deployments(namespace).Get(ctx, "local-storage-operator", metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("deployment not found yet")
			}
			return err
		}
		if deployment.Status.AvailableReplicas < 1 {
			return fmt.Errorf("waiting for full availability (%d/1)", deployment.Status.AvailableReplicas)
		}
		return nil
	}, hourTimeout, retryInterval).Should(Succeed(), "waiting for operator to be ready")

	ns, err := f.KubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "getting namespace")
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	if ns.Labels["openshift.io/cluster-monitoring"] != "true" {
		f.Logf("Enabling cluster-monitoring for namespace %s", namespace)
		ns.Labels["openshift.io/cluster-monitoring"] = "true"
		_, err = f.KubeClient.CoreV1().Namespaces().Update(context.TODO(), ns, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "enabling namespace metrics")
	}

	SetDefaultEventuallyTimeout(time.Minute * 10)
	SetDefaultEventuallyPollingInterval(time.Second * 2)
})
