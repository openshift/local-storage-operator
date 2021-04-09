package nodedaemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	localStaticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"

	v1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
)

func (r *DaemonReconciler) reconcileProvisionerConfigMap(
	request reconcile.Request,
	lvSets []localv1alpha1.LocalVolumeSet,
	lvs []v1.LocalVolume,
	ownerRefs []metav1.OwnerReference,
) (*corev1.ConfigMap, controllerutil.OperationResult, error) {
	// object meta
	objectMeta := metav1.ObjectMeta{
		Name:      common.ProvisionerConfigMapName,
		Namespace: request.Namespace,
		Labels:    map[string]string{"app": common.ProvisionerConfigMapName},
	}
	configMap := &corev1.ConfigMap{ObjectMeta: objectMeta}

	// config data
	storageClassConfig := make(map[string]localStaticProvisioner.MountConfig)
	for _, lvSet := range lvSets {
		storageClassName := lvSet.Spec.StorageClassName
		symlinkDir := path.Join(common.GetLocalDiskLocationPath(), storageClassName)
		mountConfig := localStaticProvisioner.MountConfig{
			FsType:     lvSet.Spec.FSType,
			HostDir:    symlinkDir,
			MountDir:   symlinkDir,
			VolumeMode: string(lvSet.Spec.VolumeMode),
		}
		storageClassConfig[storageClassName] = mountConfig
	}
	for _, lv := range lvs {
		for _, devices := range lv.Spec.StorageClassDevices {
			storageClassName := devices.StorageClassName
			symlinkDir := path.Join(common.GetLocalDiskLocationPath(), storageClassName)
			mountConfig := localStaticProvisioner.MountConfig{
				FsType:     devices.FSType,
				HostDir:    symlinkDir,
				MountDir:   symlinkDir,
				VolumeMode: string(devices.VolumeMode),
			}
			storageClassConfig[storageClassName] = mountConfig
		}
	}
	// create or update
	opResult, err := controllerutil.CreateOrUpdate(context.TODO(), r.client, configMap, func() error {
		if configMap.CreationTimestamp.IsZero() {
			configMap.ObjectMeta = objectMeta
		}
		configMap.ObjectMeta.Labels = objectMeta.Labels
		configMap.ObjectMeta.OwnerReferences = ownerRefs
		data, err := localStaticProvisioner.VolumeConfigToConfigMapData(&localStaticProvisioner.ProvisionerConfiguration{
			StorageClassConfig: storageClassConfig,
			NodeLabelsForPV:    []string{"kubernetes.io/hostname"},
		})
		if err != nil {
			return err
		}
		configMap.Data = data

		return nil
	})
	return configMap, opResult, err
}

func dataHash(data map[string]string) string {
	var entries []string
	for key, value := range data {
		entries = append(entries, fmt.Sprintf("%s-%s", key, value))
	}
	sort.Strings(entries)
	s := strings.Join(entries, "--")
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
