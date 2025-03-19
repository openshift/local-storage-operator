package e2e

import (
	"context"
	goctx "context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var pvConsumerLabel = "pv-consumer"

// eventuallyDelete objs, removing the finalizer if necessary
func eventuallyDelete(t *testing.T, removeFinalizers bool, objs ...client.Object) {
	f := framework.Global
	matcher := gomega.NewWithT(t)
	for _, obj := range objs {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			t.Fatalf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj)
		}
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		name := accessor.GetName()
		namespace := accessor.GetNamespace()
		matcher.Eventually(func() error {
			t.Logf("deleting obj %q with kind %q in ns %q", name, kind, namespace)
			err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
			if errors.IsNotFound(err) || errors.IsGone(err) {
				t.Logf("object already deleted: %s", err)
				return nil
			}
			accessor, err = meta.Accessor(obj)
			if err != nil {
				t.Fatalf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj)
			}
			finalizers := accessor.GetFinalizers()
			if len(finalizers) > 0 {
				t.Logf("object has finalizers: %+v", finalizers)
			}
			propPolicy := dynclient.PropagationPolicy(metav1.DeletePropagationBackground)
			if removeFinalizers {
				propPolicy = dynclient.PropagationPolicy(metav1.DeletePropagationForeground)
				accessor.SetFinalizers([]string{})
				err = f.Client.Update(context.TODO(), obj)
				if errors.IsNotFound(err) || errors.IsGone(err) {
					t.Logf("object already deleted: %s", err)
					return nil
				} else if err != nil {
					return fmt.Errorf("could not remove finalizers: %+v", finalizers)
				}
				accessor, err = meta.Accessor(obj)
				if err != nil {
					t.Fatalf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj)
				}
				// recheck finalizers
				finalizers = accessor.GetFinalizers()
				if len(finalizers) > 0 {
					return fmt.Errorf("could not remove finalizers: %+v", finalizers)
				}

			}
			err = f.Client.Delete(context.TODO(), obj, propPolicy)
			if errors.IsNotFound(err) || errors.IsGone(err) {
				t.Logf("object already deleted: %s", err)
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

// waits for PVs of the same name to become available
func eventuallyFindAvailablePVs(t *testing.T, f *framework.Framework, storageClassName string, expectedPVs []corev1.PersistentVolume) []corev1.PersistentVolume {
	matcher := gomega.NewWithT(t)
	var newPVs []corev1.PersistentVolume
	matcher.Eventually(func() bool {
		newPVs = eventuallyFindPVs(t, f, storageClassName, len(expectedPVs))
		for _, pv := range expectedPVs {
			pvFound := false
			for _, newPV := range newPVs {
				if pv.Name == newPV.Name {
					if newPV.Status.Phase == corev1.VolumeAvailable {
						pvFound = true
					} else {
						t.Logf("PV is in phase %q, waiting for it to be in phase %q", newPV.Status.Phase, corev1.VolumeAvailable)
					}
					break
				}
			}
			// expect to find each pv
			if !pvFound {
				return false
			}
		}
		return true
	}, time.Minute*8, time.Second*25).Should(gomega.BeTrue(), "waiting for PVs to become available again")
	return newPVs
}

func consumePV(t *testing.T, ctx *framework.Context, pv corev1.PersistentVolume) (*corev1.PersistentVolumeClaim, *batchv1.Job, *corev1.Pod) {
	t.Logf("consuming PV: %q", pv.Name)
	matcher := gomega.NewWithT(t)
	f := framework.Global
	name := fmt.Sprintf("%s-consumer", pv.ObjectMeta.Name)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.OperatorNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeMode:       pv.Spec.VolumeMode,
			VolumeName:       pv.Name,
			StorageClassName: &pv.Spec.StorageClassName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: pv.Spec.Capacity[corev1.ResourceStorage],
				},
			},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.OperatorNamespace,
			Labels: map[string]string{
				"app":     pvConsumerLabel,
				"pv-name": pv.Name,
			},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     pvConsumerLabel,
						"pv-name": pv.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   framework.BusyBoxImage,
							Command: []string{"/bin/sh", "-c"},
							Args: []string{
								"dd if=/dev/random of=/tmp/random.img bs=512 count=1",     // create a new file named random.img
								"md5VAR1=$(md5sum /tmp/random.img | awk '{ print $1 }')",  // calculate md5sum of random.img
								"cp /tmp/random.img /data/random.img",                     // copy random.img file to pvc mountpoint
								"md5VAR2=$(md5sum /data/random.img | awk '{ print $1 }')", // recalculate md5sum of file random.img stored in pvc
								"if [[ \"$md5VAR1\" != \"$md5VAR2\" ]];then exit 1; fi",   // verifies that the md5sum hasn't changed
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "volume-to-debug",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvc.Name,
								},
							},
						},
					},
				},
			},
		},
	}
	volName := "volume-to-debug"
	volPath := "/data"
	if pv.Spec.VolumeMode != nil && *pv.Spec.VolumeMode == corev1.PersistentVolumeBlock {
		job.Spec.Template.Spec.Containers[0].VolumeDevices = append(job.Spec.Template.Spec.Containers[0].VolumeDevices, corev1.VolumeDevice{DevicePath: volPath, Name: volName})
	} else {
		job.Spec.Template.Spec.Containers[0].VolumeMounts = append(job.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{MountPath: volPath, Name: volName})
	}

	// create pvc
	matcher.Eventually(func() error {
		t.Logf("creating pvc: %q", pvc.Name)
		return f.Client.Create(goctx.TODO(), pvc, &framework.CleanupOptions{TestContext: ctx})
	}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "creating pvc")

	// recording a time before the job was created
	toRound := time.Now()
	// rounding down to the same granularity as the timestamp
	timeStarted := time.Date(toRound.Year(), toRound.Month(), toRound.Day(), toRound.Hour(), toRound.Minute(), 0, 0, toRound.Location())

	// create consuming job
	matcher.Eventually(func() error {
		t.Logf("creating job: %q", job.Name)
		return f.Client.Create(goctx.TODO(), job, &framework.CleanupOptions{TestContext: ctx})
	}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "creating job")

	// wait for job to complete
	matcher.Eventually(func() int32 {
		t.Log("waiting for job to complete")
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, job)
		if err != nil {
			t.Logf("error fetching job: %+v", err)
			return 0
		}
		t.Logf("job completions: %d", job.Status.Succeeded)
		return job.Status.Succeeded
	}, time.Minute*5, time.Second*4).Should(gomega.BeNumerically(">=", 1), "waiting for job to complete")

	// return pods because they have to be deleted before pv is released
	podList := &corev1.PodList{}
	var matchingPod corev1.Pod
	matcher.Eventually(func() error {
		t.Logf("looking for the completed pod")
		appLabelReq, err := labels.NewRequirement("app", selection.Equals, []string{pvConsumerLabel})
		if err != nil {
			t.Logf("failed to compose labelselector 'app' requirement: %+v", err)
			return err
		}
		pvNameReq, err := labels.NewRequirement("pv-name", selection.Equals, []string{pv.Name})
		if err != nil {
			t.Logf("failed to compose labelselector 'pv-name' requirement: %+v", err)
			return err
		}
		selector := labels.NewSelector().Add(*appLabelReq).Add(*pvNameReq)
		err = f.Client.List(goctx.TODO(), podList, dynclient.MatchingLabelsSelector{Selector: selector})
		if err != nil {
			t.Logf("failed to list pods: %+v", err)
			return err
		}
		podNameList := make([]string, 0)
		for _, pod := range podList.Items {
			podNameList = append(podNameList, pod.Name)
			ts := metav1.NewTime(timeStarted)
			if pod.CreationTimestamp.After(timeStarted) || pod.CreationTimestamp.Equal(&ts) {
				matchingPod = pod
				return nil
			} else {
				t.Logf("pod is old: %q created at %v before e2e started at %v, skipping", pod.Name, timeStarted, pod.CreationTimestamp)
			}
		}
		t.Logf("could not find pod created after %q to consume PV %q in podList: %+v", timeStarted, pv.Name, podNameList)
		return fmt.Errorf("could not find pod")

	}).ShouldNot(gomega.HaveOccurred(), "fetching consuming pod")

	matchingPod.TypeMeta.Kind = "Pod"
	return pvc, job, &matchingPod
}
