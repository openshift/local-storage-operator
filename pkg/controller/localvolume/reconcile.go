package localvolume

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *ReconcileLocalVolume) deregisterLVFromStorageClass(lv localv1.LocalVolume) {
	// store a one to many association from storageClass to LocalVolumeSet
	for _, storageClassDeviceSet := range lv.Spec.StorageClassDevices {
		r.lvMap.DeregisterStorageClassOwner(storageClassDeviceSet.StorageClassName, types.NamespacedName{Name: lv.Name, Namespace: lv.Namespace})
	}
}

func (r *ReconcileLocalVolume) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	klog.Info("Reconciling LocalVolume")
	localStorageProvider := &localv1.LocalVolume{}

	err := r.client.Get(context.TODO(), request.NamespacedName, localStorageProvider)
	if err != nil {
		if errors.IsNotFound(err) {
			r.deregisterLVFromStorageClass(*localStorageProvider)
			// Requested object not found, could have been deleted after reconcile request.
			klog.Info("requested LocalVolume CR is not found, could have been deleted after the reconcile request")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{Requeue: true}, err
	}
	// store a one to many association from storageClass to LocalVolumeSet
	for _, storageClassDeviceSet := range localStorageProvider.Spec.StorageClassDevices {
		r.lvMap.RegisterStorageClassOwner(storageClassDeviceSet.StorageClassName, request.NamespacedName)
	}

	r.syncLocalVolumeProvider(localStorageProvider)
	return reconcile.Result{}, nil
}

func (r *ReconcileLocalVolume) syncLocalVolumeProvider(instance *localv1.LocalVolume) error {
	var err error
	// Create a copy so as we don't modify original LocalVolume
	o := instance.DeepCopy()

	// set default image version etc
	o.SetDefaults()

	if isDeletionCandidate(o, localVolumeFinalizer) {
		return r.cleanupLocalVolumeDeployment(o)
	}

	// Lets add a finalizer to the LocalVolume object first
	o, modified := addFinalizer(o)
	if modified {
		return r.apiClient.updateLocalVolume(o)
	}

	if o.Spec.ManagementState != operatorv1.Managed && o.Spec.ManagementState != operatorv1.Force {
		klog.Infof("operator is not managing local volumes: %v", o.Spec.ManagementState)
		o.Status.State = o.Spec.ManagementState
		err = r.apiClient.syncStatus(instance, o)
		if err != nil {
			return fmt.Errorf("error syncing status: %v", err)
		}
		return nil
	}

	err = r.syncStorageClass(o)
	if err != nil {
		klog.Errorf("failed to create storageClass: %v", err)
		return r.addFailureCondition(instance, o, err)
	}

	children := []operatorv1.GenerationStatus{}

	diskMakerDS := &appsv1.DaemonSet{}
	key := types.NamespacedName{Name: nodedaemon.DiskMakerName, Namespace: o.ObjectMeta.Namespace}
	err = r.client.Get(context.TODO(), key, diskMakerDS)
	if err != nil {
		klog.Errorf("failed to fetch diskmaker daemonset %v", err)
		return r.addFailureCondition(instance, o, err)
	}

	if diskMakerDS != nil {
		children = append(children, operatorv1.GenerationStatus{
			Group:          appsv1.GroupName,
			Resource:       "DaemonSet",
			Namespace:      diskMakerDS.Namespace,
			Name:           diskMakerDS.Name,
			LastGeneration: diskMakerDS.Generation,
		})
	}

	o.Status.Generations = children
	o.Status.State = operatorv1.Managed
	o = r.addSuccessCondition(o)
	o.Status.ObservedGeneration = &o.Generation
	err = r.apiClient.syncStatus(instance, o)
	if err != nil {
		klog.Errorf("error syncing status: %v", err)
		return fmt.Errorf("error syncing status: %v", err)
	}
	return nil
}

func (r *ReconcileLocalVolume) addFailureCondition(oldLv *localv1.LocalVolume, lv *localv1.LocalVolume, err error) error {
	message := fmt.Sprintf("error syncing local storage: %+v", err)
	condition := operatorv1.OperatorCondition{
		Type:               operatorv1.OperatorStatusTypeAvailable,
		Status:             operatorv1.ConditionFalse,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	newConditions := []operatorv1.OperatorCondition{condition}
	lv.Status.Conditions = newConditions
	syncErr := r.apiClient.syncStatus(oldLv, lv)
	if syncErr != nil {
		klog.Errorf("error syncing condition: %v", syncErr)
	}
	return err
}

func (r *ReconcileLocalVolume) addSuccessCondition(lv *localv1.LocalVolume) *localv1.LocalVolume {
	condition := operatorv1.OperatorCondition{
		Type:               operatorv1.OperatorStatusTypeAvailable,
		Status:             operatorv1.ConditionTrue,
		Message:            "Ready",
		LastTransitionTime: metav1.Now(),
	}
	newConditions := []operatorv1.OperatorCondition{condition}
	oldConditions := lv.Status.Conditions
	for _, c := range oldConditions {
		// if operator already has success condition - don't add again
		if c.Type == operatorv1.OperatorStatusTypeAvailable &&
			c.Status == operatorv1.ConditionTrue &&
			c.Message == "Ready" {
			return lv
		}
	}
	lv.Status.Conditions = newConditions
	return lv
}

func (r *ReconcileLocalVolume) cleanupLocalVolumeDeployment(lv *localv1.LocalVolume) error {
	klog.Infof("Deleting localvolume: %s", commontypes.LocalVolumeKey(lv))
	childPersistentVolumes, err := r.apiClient.listPersistentVolumes(metav1.ListOptions{LabelSelector: commontypes.GetPVOwnerSelector(lv).String()})
	if err != nil {
		msg := fmt.Sprintf("error listing persistent volumes for localvolume %s: %v", commontypes.LocalVolumeKey(lv), err)
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, listingPersistentVolumesFailed, msg)
		return fmt.Errorf(msg)
	}
	boundPVs := []corev1.PersistentVolume{}
	for _, pv := range childPersistentVolumes.Items {
		if pv.Status.Phase == corev1.VolumeBound {
			boundPVs = append(boundPVs, pv)
		}
	}
	if len(boundPVs) > 0 {
		msg := fmt.Sprintf("localvolume %s has bound persistentvolumes in use", commontypes.LocalVolumeKey(lv))
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, localVolumeDeletionFailed, msg)
		return fmt.Errorf(msg)
	}

	err = r.removeUnExpectedStorageClasses(lv, sets.NewString())
	if err != nil {
		msg := err.Error()
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, deletingStorageClassFailed, msg)
		return err
	}

	lv = removeFinalizer(lv)
	return r.apiClient.updateLocalVolume(lv)
}

func (r *ReconcileLocalVolume) syncStorageClass(cr *localv1.LocalVolume) error {
	storageClassDevices := cr.Spec.StorageClassDevices
	expectedStorageClasses := sets.NewString()
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		expectedStorageClasses.Insert(storageClassName)
		storageClass := generateStorageClass(cr, storageClassName)
		_, _, err := r.apiClient.applyStorageClass(storageClass)
		if err != nil {
			return fmt.Errorf("error creating storageClass %s: %v", storageClassName, err)
		}
	}
	removeErrors := r.removeUnExpectedStorageClasses(cr, expectedStorageClasses)
	// For now we will ignore errors while removing unexpected storageClasses
	if removeErrors != nil {
		klog.Errorf("error removing unexpected storageclasses: %v", removeErrors)
	}
	return nil
}

func (r *ReconcileLocalVolume) removeUnExpectedStorageClasses(cr *localv1.LocalVolume, expectedStorageClasses sets.String) error {
	list, err := r.apiClient.listStorageClasses(metav1.ListOptions{LabelSelector: getOwnerLabelSelector(cr).String()})
	if err != nil {
		return fmt.Errorf("error listing storageclasses for CR %s: %v", cr.Name, err)
	}
	removeErrors := []error{}
	for _, sc := range list.Items {
		if !expectedStorageClasses.Has(sc.Name) {
			klog.Infof("removing storageClass %s", sc.Name)
			scDeleteErr := r.client.Delete(context.TODO(), sc.DeepCopyObject())
			if scDeleteErr != nil && !errors.IsNotFound(scDeleteErr) {
				removeErrors = append(removeErrors, fmt.Errorf("error deleting storageclass %s: %v", sc.Name, scDeleteErr))
			}
		}
	}
	return utilerrors.NewAggregate(removeErrors)
}

func addFinalizer(lv *localv1.LocalVolume) (*localv1.LocalVolume, bool) {
	currentFinalizers := lv.GetFinalizers()
	if contains(currentFinalizers, localVolumeFinalizer) {
		return lv, false
	}
	lv.SetFinalizers(append(currentFinalizers, localVolumeFinalizer))
	return lv, true
}

func removeFinalizer(lv *localv1.LocalVolume) *localv1.LocalVolume {
	currentFinalizers := lv.GetFinalizers()
	if !contains(currentFinalizers, localVolumeFinalizer) {
		return lv
	}
	newFinalizers := remove(currentFinalizers, localVolumeFinalizer)
	lv.SetFinalizers(newFinalizers)
	return lv
}

func addOwner(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) {
	trueVal := true
	meta.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: localv1.SchemeGroupVersion.String(),
			Kind:       localv1.LocalVolumeKind,
			Name:       cr.Name,
			UID:        cr.UID,
			Controller: &trueVal,
		},
	}
}

func addOwnerLabels(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) bool {
	changed := false
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
		changed = true
	}
	if v, exists := meta.Labels[ownerNamespaceLabel]; !exists || v != cr.Namespace {
		meta.Labels[ownerNamespaceLabel] = cr.Namespace
		changed = true
	}
	if v, exists := meta.Labels[ownerNameLabel]; !exists || v != cr.Name {
		meta.Labels[ownerNameLabel] = cr.Name
		changed = true
	}

	return changed
}

func generateStorageClass(cr *localv1.LocalVolume, scName string) *storagev1.StorageClass {
	deleteReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	firstConsumerBinding := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: scName,
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		ReclaimPolicy:     &deleteReclaimPolicy,
		VolumeBindingMode: &firstConsumerBinding,
	}
	addOwnerLabels(&sc.ObjectMeta, cr)
	return sc
}

func getOwnerLabelSelector(cr *localv1.LocalVolume) labels.Selector {
	ownerLabels := labels.Set{
		ownerNamespaceLabel: cr.Namespace,
		ownerNameLabel:      cr.Name,
	}
	return labels.SelectorFromSet(ownerLabels)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// isDeletionCandidate checks if object is candidate to be deleted
func isDeletionCandidate(obj metav1.Object, finalizer string) bool {
	return obj.GetDeletionTimestamp() != nil && contains(obj.GetFinalizers(), finalizer)
}
