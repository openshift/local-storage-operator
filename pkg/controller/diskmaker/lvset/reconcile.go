package lvset

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"time"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/controller/util"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileLocalVolumeSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling LocalVolumeSet")

	// Fetch the LocalVolumeSet instance
	instance := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// get associated config
	cm := corev1.ConfigMap{}
	provisionerConfig := staticProvisioner.ProvisionerConfiguration{}
	staticProvisioner.ConfigMapDataToVolumeConfig(cm.Data, &provisionerConfig)
	// find disks that match lvset filter

	storageClass := instance.Spec.StorageClassName

	blockDevices, err := internal.ListBlockDevices()
	if err != nil {
		return reconcile.Result{}, nil
	}

DeviceLoop:
	for _, blockDevice := range blockDevices {
		devLogger := reqLogger.WithValues("Device.Name", blockDevice.Name)
		for _, filter := range filters {
			valid, msg, err := filter.isMatch(blockDevice)
			if err != nil {
				devLogger.Error(err, "filter error: %q", msg)
				valid = false
				continue DeviceLoop
			} else if !valid {
				devLogger.Info("filter negative: %q", msg)
				continue DeviceLoop
			}
		}
		for _, matcher := range matchers {
			valid, msg, err := matcher.isMatch(blockDevice, *instance.Spec.DeviceInclusionSpec)
			if err != nil {
				devLogger.Error(err, "match error: %q", msg)
				valid = false
				continue DeviceLoop
			} else if !valid {
				devLogger.Info("match negative: %q", msg)
				continue DeviceLoop
			}
		}
		// handle valid disk
		err = symLinkDisk(blockDevice, path.Join(util.GetLocalDiskLocationPath(), storageClass))
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "could not symlink disk")
		}

	}

	return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
}

func symLinkDisk(dev internal.BlockDevice, symLinkDir string) error {
	pathByID, err := dev.GetPathByID()
	if err != nil {
		return err
	}
	// ensure symLinkDirExists
	err = os.MkdirAll(symLinkDir, 0755)
	if err != nil {
		return errors.Wrap(err, "could not create symlinkdir")
	}
	deviceID := filepath.Base(pathByID)
	symLinkPath := path.Join(symLinkDir, deviceID)
	// create symlink
	err = os.Symlink(pathByID, symLinkPath)
	if err != nil {
		return err
	}

	return nil
}
