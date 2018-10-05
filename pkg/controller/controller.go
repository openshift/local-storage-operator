package controller

import (
	"context"
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Handler struct {
	// Fill me
	localStorageNameSpace string
	defaultLabels         map[string]string
}

type localDiskData map[string]map[string]string

// NewHandler returns a controller handler
func NewHandler(namespace string) sdk.Handler {
	return &Handler{
		localStorageNameSpace: namespace,
	}
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.LocalStorageProvider:
		err := sdk.Create(newbusyBoxPod(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create busybox pod : %v", err)
			return err
		}
	}
	return nil
}

// CreateConfigMap Create configmap requires by the local storage provisioner
func (h *Handler) CreateProvisionerConfigMap(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	configMapName := cr.Name + "-local-provisioner-configmap"
	configMapData := make(localDiskData)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		storageClassData := map[string]string{}
		storageClassData["fstype"] = storageClassDevice.FSType
		storageClassData["volumeMode"] = string(storageClassDevice.VolumeMode)
		storageClassData["hostDir"] = fmt.Sprintf("/mnt/local-storage/%s", storageClassName)
		storageClassData["mountDir"] = fmt.Sprintf("/mnt/local-storage/%s", storageClassName)
		configMapData[storageClassName] = storageClassData
	}
	configmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   configMapName,
			Labels: h.defaultLabels,
		},
	}
	y, err := yaml.Marshal(configMapData)
	if err != nil {
		return nil, fmt.Errorf("error creating configmap while marshalling yaml %v", err)
	}
	configmap.Data = map[string]string{
		"storageClassMap": string(y),
	}
	return configmap, nil
}

func (h *Handler) CreateDiskMakerConfig(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	configMapName := cr.Name + "-diskmaker-configmap"
	configMapData := make(diskmaker.DiskConfig)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		disks := new(diskmaker.Disks)
		if len(storageClassDevice.DeviceNames) > 0 {
			disks.DiskNames = storageClassDevice.DeviceNames
		} else {
			disks.DiskPatterns = storageClassDevice.DeviceWhitelistPattern
		}
		configMapData[storageClassDevice.StorageClassName] = disks
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Labels:    h.defaultLabels,
			Namespace: h.localStorageNameSpace,
		},
	}
	yaml, err := configMapData.ToYAML()
	if err != nil {
		return nil, err
	}
	configMap.Data = map[string]string{
		"diskMakerConfig": yaml,
	}
	return configMap, nil
}

// newbusyBoxPod demonstrates how to create a busybox pod
func newbusyBoxPod(cr *v1alpha1.LocalStorageProvider) *corev1.Pod {
	labels := map[string]string{
		"app": "busy-box",
	}
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "busy-box",
			Namespace: cr.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cr, schema.GroupVersionKind{
					Group:   v1alpha1.SchemeGroupVersion.Group,
					Version: v1alpha1.SchemeGroupVersion.Version,
					Kind:    "LocalStorageProvider",
				}),
			},
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "busybox",
					Image:   "docker.io/busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}
