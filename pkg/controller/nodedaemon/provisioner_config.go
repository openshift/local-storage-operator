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

	localStaticProvisioner "github.com/openshift/sig-storage-local-static-provisioner/pkg/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
)

func (r *DaemonReconciler) reconcileProvisionerConfigMap(
	request reconcile.Request,
	lvSets []localv1alpha1.LocalVolumeSet,
	ownerRefs []metav1.OwnerReference,
) (*corev1.ConfigMap, controllerutil.OperationResult, error) {
	// object meta
	objectMeta := metav1.ObjectMeta{
		Name:      ProvisionerConfigMapName,
		Namespace: request.Namespace,
		Labels:    map[string]string{"app": ProvisionerConfigMapName},
	}
	configMap := &corev1.ConfigMap{ObjectMeta: objectMeta}

	// config data
	storageClassConfig := make(map[string]localStaticProvisioner.MountConfig)
	for _, lvSet := range lvSets {
		storageClassName := lvSet.Spec.StorageClassName
		symlinkDir := path.Join(common.GetLocalDiskLocationPath(), storageClassName)
		mountConfig := localStaticProvisioner.MountConfig{
			BlockCleanerCommand: []string{localStaticProvisioner.DefaultBlockCleanerCommand},
			FsType:              lvSet.Spec.FSType,
			HostDir:             symlinkDir,
			MountDir:            symlinkDir,
			VolumeMode:          string(lvSet.Spec.VolumeMode),
		}
		storageClassConfig[storageClassName] = mountConfig
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
