package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var pvConsumerLabel = "pv-consumer"
var (
	retryInterval   = time.Second * 5
	hourTimeout     = time.Hour
	deletionTimeout = 10 * time.Minute
)

// eventuallyDelete objs, removing the finalizer if necessary
func eventuallyDelete(objs ...client.Object) {
	f := framework.Global
	for _, obj := range objs {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			Fail(fmt.Sprintf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj))
		}
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		name := accessor.GetName()
		namespace := accessor.GetNamespace()
		Eventually(func(ctx context.Context) error {
			f.Logf("deleting obj %q with kind %q in ns %q", name, kind, namespace)
			err := f.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
			if errors.IsNotFound(err) || errors.IsGone(err) {
				f.Logf("object already deleted: %s", err)
				return nil
			}
			accessor, err = meta.Accessor(obj)
			if err != nil {
				Fail(fmt.Sprintf("deletion failed, cannot get accessor for object: %+v, obj: %+v", err, obj))
			}
			finalizers := accessor.GetFinalizers()
			if len(finalizers) > 0 {
				f.Logf("object has finalizers: %+v", finalizers)
			}
			propPolicy := dynclient.PropagationPolicy(metav1.DeletePropagationBackground)
			err = f.Client.Delete(ctx, obj, propPolicy)
			if errors.IsNotFound(err) || errors.IsGone(err) {
				f.Logf("object already deleted: %s", err)
				return nil
			}
			return err
		}, time.Minute*5, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "deleting %q", name)
	}
}

// PVs rapidly get deleted and recreated if diskmaker is running
// using eventuallyDelete is inherently racy
// this function compares if PV was recreated with a different UID
// then PV must be deleted.
func eventuallyDeletePV(pv *corev1.PersistentVolume) {
	f := framework.Global
	oldUID := pv.UID
	Eventually(func(ctx context.Context) error {
		f.Logf("deleting obj %q with kind %q in ns %q", pv.Name, pv.Kind, pv.Namespace)
		err := f.Client.Get(ctx, types.NamespacedName{Name: pv.Name, Namespace: pv.Namespace}, pv)
		if errors.IsNotFound(err) || errors.IsGone(err) {
			f.Logf("object already deleted: %s", err)
			return nil
		}
		newUID := pv.GetUID()
		if newUID != oldUID {
			f.Logf("object %s has been deleted and recreated", pv.Name)
			return nil
		}

		err = f.Client.Delete(ctx, pv)
		if errors.IsNotFound(err) || errors.IsGone(err) {
			f.Logf("object already deleted: %s", err)
			return nil
		}
		return err
	}, time.Minute*5, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "deleting %q", pv.Name)
}

func eventuallyFindPVs(f *framework.Framework, storageClassName string, expectedPVs int) []corev1.PersistentVolume {
	var matchedPVs []corev1.PersistentVolume
	Eventually(func() []corev1.PersistentVolume {
		pvList := &corev1.PersistentVolumeList{}

		Eventually(func(ctx context.Context) error {
			return f.Client.List(ctx, pvList)
		}).WithContext(context.Background()).ShouldNot(HaveOccurred())

		matchedPVs = make([]corev1.PersistentVolume, 0)
		for _, pv := range pvList.Items {
			if pv.Spec.StorageClassName == storageClassName {
				matchedPVs = append(matchedPVs, pv)
			}
		}
		f.Logf("waiting for %d PVs to be created with StorageClass: %q, found %d", expectedPVs, storageClassName, len(matchedPVs))

		return matchedPVs
	}, time.Minute*5, time.Second*8).Should(HaveLen(expectedPVs), "checking number of PVs for for storageclass: %q", storageClassName)
	return matchedPVs

}

func eventuallyFindLVDLsForPVs(f *framework.Framework, namespace string, pvNames []string) []localv1.LocalVolumeDeviceLink {
	pvNameSet := sets.New(pvNames...)
	foundLVDLs := make([]localv1.LocalVolumeDeviceLink, 0)
	Eventually(func(ctx context.Context) bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(ctx, lvdlList, client.InNamespace(namespace))
		if err != nil {
			f.Logf("error listing LocalVolumeDeviceLink objects: %v", err)
			return false
		}
		foundLVDLs = foundLVDLs[:0]
		for _, lvdl := range lvdlList.Items {
			if ok := pvNameSet.Has(lvdl.Spec.PersistentVolumeName); ok {
				// Wait until status fields are populated by the status update
				if lvdl.Status.CurrentLinkTarget == "" || lvdl.Status.PreferredLinkTarget == "" || lvdl.Status.PersistentVolumeSymlinkPath == "" {
					return false
				}
				foundLVDLs = append(foundLVDLs, lvdl)
			}
		}
		return len(foundLVDLs) == len(pvNameSet)
	}, time.Minute*5, time.Second*5).WithContext(context.Background()).Should(BeTrue(), "waiting for LVDL objects with populated status for all PVs")
	return foundLVDLs
}

// waits for PVs of the same name to become available
func eventuallyFindAvailablePVs(f *framework.Framework, storageClassName string, expectedPVs []corev1.PersistentVolume) []corev1.PersistentVolume {
	var newPVs []corev1.PersistentVolume
	Eventually(func() bool {
		newPVs = eventuallyFindPVs(f, storageClassName, len(expectedPVs))
		for _, pv := range expectedPVs {
			pvFound := false
			for _, newPV := range newPVs {
				if pv.Name == newPV.Name {
					if newPV.Status.Phase == corev1.VolumeAvailable {
						pvFound = true
					} else {
						f.Logf("PV is in phase %q, waiting for it to be in phase %q", newPV.Status.Phase, corev1.VolumeAvailable)
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
	}, time.Minute*8, time.Second*25).Should(BeTrue(), "waiting for PVs to become available again")
	return newPVs
}

func consumePV(namespace string, pv corev1.PersistentVolume) (*corev1.PersistentVolumeClaim, *batchv1.Job, *corev1.Pod) {
	f := framework.Global
	f.Logf("consuming PV: %q", pv.Name)
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
	Eventually(func(ctx context.Context) error {
		f.Logf("creating pvc: %q", pvc.Name)
		return f.Client.Create(ctx, pvc, nil)
	}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating pvc")

	DeferCleanup(func() {
		deleteResource(pvc, pvc.Namespace, pvc.Name, f.Client)
	})

	// recording a time before the job was created
	toRound := time.Now()
	// rounding down to the same granularity as the timestamp
	timeStarted := time.Date(toRound.Year(), toRound.Month(), toRound.Day(), toRound.Hour(), toRound.Minute(), 0, 0, toRound.Location())

	// create consuming job
	Eventually(func(ctx context.Context) error {
		f.Logf("creating job: %q", job.Name)
		return f.Client.Create(ctx, job, nil)
	}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating job")

	DeferCleanup(func() {
		deleteResource(job, job.Namespace, job.Name, f.Client)
	})

	// wait for job to complete
	Eventually(func(ctx context.Context) int32 {
		f.Logf("waiting for job to complete")
		err := f.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, job)
		if err != nil {
			f.Logf("error fetching job: %+v", err)
			return 0
		}
		f.Logf("job completions: %d", job.Status.Succeeded)
		return job.Status.Succeeded
	}, time.Minute*5, time.Second*4).WithContext(context.Background()).Should(BeNumerically(">=", 1), "waiting for job to complete")

	// return pods because they have to be deleted before pv is released
	podList := &corev1.PodList{}
	var matchingPod corev1.Pod
	Eventually(func(ctx context.Context) error {
		f.Logf("looking for the completed pod")
		appLabelReq, err := labels.NewRequirement("app", selection.Equals, []string{pvConsumerLabel})
		if err != nil {
			f.Logf("failed to compose labelselector 'app' requirement: %+v", err)
			return err
		}
		pvNameReq, err := labels.NewRequirement("pv-name", selection.Equals, []string{pv.Name})
		if err != nil {
			f.Logf("failed to compose labelselector 'pv-name' requirement: %+v", err)
			return err
		}
		selector := labels.NewSelector().Add(*appLabelReq).Add(*pvNameReq)
		err = f.Client.List(ctx, podList, dynclient.MatchingLabelsSelector{Selector: selector})
		if err != nil {
			f.Logf("failed to list pods: %+v", err)
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
				f.Logf("pod is old: %q created at %v before e2e started at %v, skipping", pod.Name, timeStarted, pod.CreationTimestamp)
			}
		}
		f.Logf("could not find pod created after %q to consume PV %q in podList: %+v", timeStarted, pv.Name, podNameList)
		return fmt.Errorf("could not find pod")

	}).WithContext(context.Background()).ShouldNot(HaveOccurred(), "fetching consuming pod")

	matchingPod.TypeMeta.Kind = "Pod"
	return pvc, job, &matchingPod
}

func assertLVDLsContainTargetAndNodes(lvdls []localv1.LocalVolumeDeviceLink, expectedTarget string, expectedNodeNames []string) {
	foundNodeNames := sets.New[string]()
	for _, lvdl := range lvdls {
		Expect(lvdl.Status.ValidLinkTargets).To(ContainElement(expectedTarget),
			"expected ValidLinkTargets for LVDL %q to contain %q", lvdl.Name, expectedTarget)
		Expect(lvdl.Spec.NodeName).ToNot(BeEmpty(), "expected NodeName for LVDL %q", lvdl.Name)
		Expect(lvdl.Status.PersistentVolumeSymlinkPath).ToNot(BeEmpty(), "expected PersistentVolumeSymlinkPath for LVDL %q", lvdl.Name)
		foundNodeNames.Insert(lvdl.Spec.NodeName)
	}
	Expect(foundNodeNames.UnsortedList()).To(ConsistOf(expectedNodeNames),
		"expected LVDL node names for target %q", expectedTarget)
}

// assertLVDLSymlinkPathMatchesPVs checks that each LVDL's status.symlinkPath matches the
// corresponding PersistentVolume's spec.local.path (same as the on-disk symlink under /mnt/local-storage).
func assertLVDLSymlinkPathMatchesPVs(lvdls []localv1.LocalVolumeDeviceLink, pvs []corev1.PersistentVolume) {
	byName := make(map[string]corev1.PersistentVolume, len(pvs))
	for _, pv := range pvs {
		byName[pv.Name] = pv
	}
	for i := range lvdls {
		lvdl := &lvdls[i]
		pv, ok := byName[lvdl.Spec.PersistentVolumeName]
		Expect(ok).To(BeTrue(), "missing PV %q for LVDL %q", lvdl.Spec.PersistentVolumeName, lvdl.Name)
		Expect(lvdl.Status.PersistentVolumeSymlinkPath).ToNot(BeEmpty(), "expected PersistentVolumeSymlinkPath for LVDL %q", lvdl.Name)
		if pv.Spec.Local != nil && pv.Spec.Local.Path != "" {
			Expect(lvdl.Status.PersistentVolumeSymlinkPath).To(Equal(pv.Spec.Local.Path),
				"LVDL %q status.symlinkPath should match PV %q spec.local.path", lvdl.Name, pv.Name)
		}
	}
}
func deleteResource(obj client.Object, namespace, name string, cl framework.FrameworkClient) error {
	pollingContext, cancel := context.WithTimeout(context.TODO(), deletionTimeout)
	defer cancel()
	err := cl.Delete(pollingContext, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return wait.PollUntilContextTimeout(pollingContext, retryInterval, hourTimeout, true, func(ctx context.Context) (bool, error) {
		objectKey := types.NamespacedName{Namespace: namespace, Name: name}
		err := cl.Get(ctx, objectKey, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}
