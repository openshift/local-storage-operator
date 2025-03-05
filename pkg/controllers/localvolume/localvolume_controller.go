/*
Copyright 2021 The Local Storage Operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package localvolume

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controllers/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type localDiskData map[string]map[string]string

const (
	componentName        = "local-storage-operator"
	localDiskLocation    = "/mnt/local-storage"
	localVolumeFinalizer = "storage.openshift.com/local-volume-protection"
)

// LocalVolumeReconciler reconciles a LocalVolume object
type LocalVolumeReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client            client.Client
	Scheme            *runtime.Scheme
	apiClient         apiUpdater
	LvMap             *common.StorageClassOwnerMap
	controllerVersion string
}

func (r *LocalVolumeReconciler) deregisterLVFromStorageClass(lv localv1.LocalVolume) {
	// store a one to many association from storageClass to LocalVolumeSet
	for _, storageClassDeviceSet := range lv.Spec.StorageClassDevices {
		r.LvMap.DeregisterStorageClassOwner(storageClassDeviceSet.StorageClassName, types.NamespacedName{Name: lv.Name, Namespace: lv.Namespace})
	}
}

//+kubebuilder:rbac:groups=local.storage.openshift.io,namespace=default,resources=*,verbs=*
//+kubebuilder:rbac:groups="",namespace=default,resources=pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets,verbs="*"
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,namespace=default,resources=roles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,namespace=default,resources=deployments;daemonsets;replicasets;statefulsets,verbs=*
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,namespace=default,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resourceNames=local-storage-operator,namespace=default,resources=deployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=*
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch

func (r *LocalVolumeReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("Reconciling LocalVolume", "namespace", request.Namespace, "name", request.Name)
	localStorageProvider := &localv1.LocalVolume{}

	err := r.Client.Get(ctx, request.NamespacedName, localStorageProvider)
	if err != nil {
		if errors.IsNotFound(err) {
			r.deregisterLVFromStorageClass(*localStorageProvider)
			// Requested object not found, could have been deleted after reconcile request.
			klog.Info("requested LocalVolume CR is not found, could have been deleted after the reconcile request")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{Requeue: true}, err
	}
	// store a one to many association from storageClass to LocalVolumeSet
	for _, storageClassDeviceSet := range localStorageProvider.Spec.StorageClassDevices {
		r.LvMap.RegisterStorageClassOwner(storageClassDeviceSet.StorageClassName, request.NamespacedName)
	}

	err = r.syncLocalVolumeProvider(ctx, localStorageProvider)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

func (r *LocalVolumeReconciler) syncLocalVolumeProvider(ctx context.Context, instance *localv1.LocalVolume) error {
	var err error
	// Create a copy so as we don't modify original LocalVolume
	o := instance.DeepCopy()

	// set default image version etc
	o.SetDefaults()

	if isDeletionCandidate(o, localVolumeFinalizer) {
		return r.cleanupLocalVolumeDeployment(ctx, o)
	}

	// Lets add a finalizer to the LocalVolume object first
	o, modified := addFinalizer(o)
	if modified {
		return r.apiClient.updateLocalVolume(o)
	}

	if o.Spec.ManagementState != operatorv1.Managed && o.Spec.ManagementState != operatorv1.Force {
		klog.InfoS("operator is not managing local volumes", "ManagementState", o.Spec.ManagementState)
		o.Status.State = o.Spec.ManagementState
		err = r.apiClient.syncStatus(instance, o)
		if err != nil {
			return fmt.Errorf("error syncing status: %v", err)
		}
		return nil
	}

	err = r.syncStorageClass(ctx, o)
	if err != nil {
		klog.ErrorS(err, "failed to create storageClass")
		return r.addFailureCondition(instance, o, err)
	}

	children := []operatorv1.GenerationStatus{}

	diskMakerDS := &appsv1.DaemonSet{}
	key := types.NamespacedName{Name: nodedaemon.DiskMakerName, Namespace: o.ObjectMeta.Namespace}
	err = r.Client.Get(ctx, key, diskMakerDS)
	if err != nil {
		klog.ErrorS(err, "failed to fetch diskmaker daemonset")
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
		klog.ErrorS(err, "error syncing status")
		return fmt.Errorf("error syncing status: %v", err)
	}
	return nil
}

func (r *LocalVolumeReconciler) addFailureCondition(oldLv *localv1.LocalVolume, lv *localv1.LocalVolume, err error) error {
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
		klog.ErrorS(syncErr, "error syncing condition")
	}
	return err
}

func (r *LocalVolumeReconciler) addSuccessCondition(lv *localv1.LocalVolume) *localv1.LocalVolume {
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

func (r *LocalVolumeReconciler) cleanupLocalVolumeDeployment(ctx context.Context, lv *localv1.LocalVolume) error {
	klog.InfoS("Deleting localvolume", "Namespace-Name", common.LocalVolumeKey(lv))
	// Release all remaining 'Available' PV's owned by the LV
	err := common.ReleaseAvailablePVs(lv, r.Client)
	if err != nil {
		msg := fmt.Sprintf("error releasing unbound persistent volumes for localvolume %s: %v", common.LocalVolumeKey(lv), err)
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, releasingPersistentVolumesFailed, msg)
		return fmt.Errorf(msg)
	}

	// finalizer should be unset only when no owned PVs are found
	ownedPVs, err := common.GetOwnedPVs(lv, r.Client)
	if err != nil {
		msg := fmt.Sprintf("error listing persistent volumes for localvolume %s: %v", common.LocalVolumeKey(lv), err)
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, listingPersistentVolumesFailed, msg)
		return fmt.Errorf(msg)
	}

	if len(ownedPVs) > 0 {
		pvNames := ""
		for _, pv := range ownedPVs {
			pvNames += fmt.Sprintf(" %v", pv.Name)
		}

		klog.InfoS("owned PVs found, not removing finalizer", "pvNames", pvNames)
		msg := fmt.Sprintf("localvolume %s has owned persistentvolumes in use", common.LocalVolumeKey(lv))
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, localVolumeDeletionFailed, msg)
		return fmt.Errorf(msg)
	}

	err = r.removeUnExpectedStorageClasses(ctx, lv, sets.NewString())
	if err != nil {
		msg := err.Error()
		r.apiClient.recordEvent(lv, corev1.EventTypeWarning, deletingStorageClassFailed, msg)
		return err
	}

	lv = removeFinalizer(lv)
	return r.apiClient.updateLocalVolume(lv)
}

func (r *LocalVolumeReconciler) syncStorageClass(ctx context.Context, cr *localv1.LocalVolume) error {
	storageClassDevices := cr.Spec.StorageClassDevices
	expectedStorageClasses := sets.NewString()
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		expectedStorageClasses.Insert(storageClassName)
		storageClass, err := generateStorageClass(cr, storageClassName)
		if err != nil {
			return fmt.Errorf("error generating storageClass %s: %v", storageClassName, err)
		}
		_, _, err = r.apiClient.applyStorageClass(ctx, storageClass)
		if err != nil {
			return fmt.Errorf("error creating storageClass %s: %v", storageClassName, err)
		}
	}
	removeErrors := r.removeUnExpectedStorageClasses(ctx, cr, expectedStorageClasses)
	// For now we will ignore errors while removing unexpected storageClasses
	if removeErrors != nil {
		klog.ErrorS(removeErrors, "error removing unexpected storageclasses")
	}
	return nil
}

func (r *LocalVolumeReconciler) removeUnExpectedStorageClasses(ctx context.Context, cr *localv1.LocalVolume, expectedStorageClasses sets.String) error {
	list, err := r.apiClient.listStorageClasses(metav1.ListOptions{LabelSelector: getOwnerLabelSelector(cr).String()})
	if err != nil {
		return fmt.Errorf("error listing storageclasses for CR %s: %v", cr.Name, err)
	}
	removeErrors := []error{}
	for _, sc := range list.Items {
		if !expectedStorageClasses.Has(sc.Name) {
			klog.InfoS("removing storageClass", "scName", sc.Name)
			scDeleteErr := r.Client.Delete(ctx, sc.DeepCopy())
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

func getOwnerRefs(cr *localv1.LocalVolume) []metav1.OwnerReference {
	trueVal := true
	return []metav1.OwnerReference{
		{
			APIVersion: localv1.GroupVersion.String(),
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
	if v, exists := meta.Labels[common.OwnerNamespaceLabel]; !exists || v != cr.Namespace {
		meta.Labels[common.OwnerNamespaceLabel] = cr.Namespace
		changed = true
	}
	if v, exists := meta.Labels[common.OwnerNameLabel]; !exists || v != cr.Name {
		meta.Labels[common.OwnerNameLabel] = cr.Name
		changed = true
	}
	if v, exists := meta.Labels[common.OwnerKindLabel]; !exists || v != localv1.LocalVolumeKind {
		meta.Labels[common.OwnerKindLabel] = localv1.LocalVolumeKind
		changed = true
	}

	return changed
}

func generateStorageClass(cr *localv1.LocalVolume, scName string) (*storagev1.StorageClass, error) {
	scBytes, err := assets.ReadFileAndReplace(
		common.LocalVolumeStorageClassTemplate,
		[]string{
			"${OBJECT_NAME}", scName,
		},
	)
	if err != nil {
		return nil, err
	}
	sc := resourceread.ReadStorageClassV1OrDie(scBytes)

	addOwnerLabels(&sc.ObjectMeta, cr)
	return sc, nil
}

func getOwnerLabelSelector(cr *localv1.LocalVolume) labels.Selector {
	ownerLabels := labels.Set{
		common.OwnerNamespaceLabel: cr.Namespace,
		common.OwnerNameLabel:      cr.Name,
		common.OwnerKindLabel:      localv1.LocalVolumeKind,
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

// SetupWithManager sets up the controller with the Manager.
func (r *LocalVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.apiClient = newAPIUpdater(mgr)
	r.LvMap = &common.StorageClassOwnerMap{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&localv1.LocalVolume{}).
		Watches(&appsv1.DaemonSet{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &localv1.LocalVolume{})).
		//  watch for storageclass, enqueue owner
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				pv, ok := obj.(*corev1.PersistentVolume)
				if !ok {
					return []reconcile.Request{}
				}

				names := r.LvMap.GetStorageClassOwners(pv.Spec.StorageClassName)
				reqs := make([]reconcile.Request, 0)
				for _, name := range names {
					reqs = append(reqs, reconcile.Request{NamespacedName: name})
				}
				return reqs
			})).
		// Watch for changes to owned resource PersistentVolume and enqueue the LocalVolume
		// so that the controller can update the status and finalizer based on the owned PVs
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				pv, ok := obj.(*corev1.PersistentVolume)
				if !ok {
					return []reconcile.Request{}
				}
				// get owner
				ownerName, found := pv.Labels[common.PVOwnerNameLabel]
				if !found {
					return []reconcile.Request{}
				}
				ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
				if !found {
					return []reconcile.Request{}
				}
				// skip LocalVolumeSet owned PVs
				ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
				if ownerKind != localv1.LocalVolumeKind || !found {
					return []reconcile.Request{}
				}
				req := reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}}
				return []reconcile.Request{req}
			})).
		Complete(r)
}
