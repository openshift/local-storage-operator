package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	framework "github.com/openshift/local-storage-operator/test-framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
)

// name is only for logging
func eventuallyDelete(t *testing.T, objs ...client.Object) {
	f := framework.Global
	matcher := gomega.NewWithT(t)
	for _, obj := range objs {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			t.Fatalf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj)
		}
		name := accessor.GetName()
		matcher.Eventually(func() error {
			t.Logf("deleting %q", name)
			err := f.Client.Delete(context.TODO(), obj)
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}, time.Minute*5, time.Second*5).ShouldNot(gomega.HaveOccurred(), "deleting %q", name)
	}

}

func eventuallyFindPVs(t *testing.T, f *framework.Framework, storageClassName string, expectedPVs int) []corev1.PersistentVolume {
	var matchedPVs []corev1.PersistentVolume
	matcher := gomega.NewWithT(t)
	matcher.Eventually(func() []corev1.PersistentVolume {
		pvList := &corev1.PersistentVolumeList{}
		t.Log(fmt.Sprintf("waiting for %d PVs to be created with StorageClass: %q", expectedPVs, storageClassName))
		matcher.Eventually(func() error {
			return f.Client.List(context.TODO(), pvList)
		}).ShouldNot(gomega.HaveOccurred())
		matchedPVs = make([]corev1.PersistentVolume, 0)
		for _, pv := range pvList.Items {
			if pv.Spec.StorageClassName == storageClassName {
				matchedPVs = append(matchedPVs, pv)
			}
		}
		return matchedPVs
	}, time.Minute*5, time.Second*8).Should(gomega.HaveLen(expectedPVs), "checking number of PVs for for storageclass: %q", storageClassName)
	return matchedPVs

}
