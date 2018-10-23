package controller

import (
	"context"
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Handler returns a Handler for running the operator
type Handler struct {
	// Fill me
	localStorageNameSpace string
	localDiskLocation     string
	provisonerConfigName  string
	diskMakerConfigName   string
}

type localDiskData map[string]map[string]string

const (
	localDiskLocation = "/mnt/local-storage"
)

// NewHandler returns a controller handler
func NewHandler(namespace string) sdk.Handler {
	handler := &Handler{
		localStorageNameSpace: namespace,
		localDiskLocation:     localDiskLocation,
	}
	return handler
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.LocalStorageProvider:
		if event.Deleted {
			// TODO: Handle deletion later
			return nil
		}
		var err error
		_, err = h.syncConfigMaps(o)
		if err != nil {
			logrus.Errorf("error creating provisioner configmap %s with %v", o.Name, err)
			return err
		}

		diskMakerConfigMap, err := h.CreateDiskMakerConfig(o)
		if err != nil {
			logrus.Errorf("error creating diskmaker configmap %s with %v", o.Name, err)
			return err
		}
		err = sdk.Create(diskMakerConfigMap)
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create configmap for diskMaker %s with %v", o.Name, err)
			return err
		}

		err = h.createStorageClass(o)
		if err != nil {
			logrus.Errorf("failed to create storageClass %v", err)
			return err
		}

		err = sdk.Create(h.createLocalProvisionerDaemonset(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create daemonset for provisioner %s with %v", o.Name, err)
			return err
		}

		err = sdk.Create(h.createDiskMakerDaemonSet(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create daemonset for diskmaker %s with %v", o.Name, err)
			return err
		}

	}
	return nil
}

func (h *Handler) syncConfigMaps(o *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	configMap, err := h.generateProvisionerConfigMap(o)
	if err != nil {
		logrus.Errorf("error creating provisioner configmap %s with %v", o.Name, err)
		return nil, err
	}
	configMap, _, err = resourceapply.ApplyConfigMap(k8sclient.GetKubeClient().CoreV1(), configMap)
	if err != nil {
		return nil, err
	}
	return configMap, nil
}

// CreateConfigMap Create configmap requires by the local storage provisioner
func (h *Handler) generateProvisionerConfigMap(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	h.provisonerConfigName = cr.Name + "-local-provisioner-configmap"
	configMapData := make(localDiskData)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		storageClassData := map[string]string{}
		storageClassData["fstype"] = storageClassDevice.FSType
		storageClassData["volumeMode"] = string(storageClassDevice.VolumeMode)
		storageClassData["hostDir"] = fmt.Sprintf("%s/%s", h.localDiskLocation, storageClassName)
		storageClassData["mountDir"] = fmt.Sprintf("%s/%s", h.localDiskLocation, storageClassName)
		configMapData[storageClassName] = storageClassData
	}
	configmap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.provisonerConfigName,
			Labels:    provisionerLabels(cr.Name),
			Namespace: cr.Namespace,
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

func (h *Handler) createStorageClass(cr *v1alpha1.LocalStorageProvider) error {
	storageClassDevices := cr.Spec.StorageClassDevices
	var err error
	for _, storageClassDevice := range storageClassDevices {
		storageClassName := storageClassDevice.StorageClassName
		storageClass := newStorageClass(cr, storageClassName)
		err = sdk.Create(storageClass)
		if err != nil && !errors.IsAlreadyExists(err) {
			return fmt.Errorf("error creating storageClass %s with %v", storageClassName, err)
		}
	}
	return nil
}

func (h *Handler) CreateDiskMakerConfig(cr *v1alpha1.LocalStorageProvider) (*corev1.ConfigMap, error) {
	h.diskMakerConfigName = cr.Name + "-diskmaker-configmap"
	configMapData := make(diskmaker.DiskConfig)
	storageClassDevices := cr.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		disks := new(diskmaker.Disks)
		if len(storageClassDevice.DeviceNames) > 0 {
			disks.DiskNames = storageClassDevice.DeviceNames
		} else if len(storageClassDevice.DeviceIDs) > 0 {
			disks.DeviceIDs = storageClassDevice.DeviceIDs
		}
		configMapData[storageClassDevice.StorageClassName] = disks
	}

	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.diskMakerConfigName,
			Labels:    diskMakerLabels(cr.Name),
			Namespace: cr.Namespace,
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

func (h *Handler) createLocalProvisionerDaemonset(cr *v1alpha1.LocalStorageProvider) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	containers := []corev1.Container{
		{
			Name:  "local-storage-provisioner",
			Image: cr.Spec.ProvisionerImage,
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "provisioner-config",
					ReadOnly:  true,
					MountPath: "/etc/provisioner/config",
				},
				{
					Name:             "local-disks",
					MountPath:        h.localDiskLocation,
					MountPropagation: &hostContainerPropagation,
				},
			},
		},
	}
	volumes := []corev1.Volume{
		{
			Name: "provisioner-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: h.provisonerConfigName,
					},
				},
			},
		},
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: h.localDiskLocation,
				},
			},
		},
	}
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "local-provisioner",
			Namespace: cr.Namespace,
			Labels:    provisionerLabels(cr.Name),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: provisionerLabels(cr.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: provisionerLabels(cr.Name),
				},
				Spec: corev1.PodSpec{
					Containers:         containers,
					ServiceAccountName: "local-storage-admin",
					Volumes:            volumes,
				},
			},
		},
	}
}

func (h *Handler) createDiskMakerDaemonSet(cr *v1alpha1.LocalStorageProvider) *appsv1.DaemonSet {
	privileged := true
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	containers := []corev1.Container{
		{
			Name:  "local-diskmaker",
			Image: cr.Spec.DiskMakerImage,
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "provisioner-config",
					ReadOnly:  true,
					MountPath: "/etc/local-storage-operator/config",
				},
				{
					Name:             "local-disks",
					MountPath:        h.localDiskLocation,
					MountPropagation: &hostContainerPropagation,
				},
			},
		},
	}
	volumes := []corev1.Volume{
		{
			Name: "provisioner-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: h.diskMakerConfigName,
					},
				},
			},
		},
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: h.localDiskLocation,
				},
			},
		},
	}
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "local-diskmaker",
			Namespace: cr.Namespace,
			Labels:    diskMakerLabels(cr.Name),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: diskMakerLabels(cr.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: diskMakerLabels(cr.Name),
				},
				Spec: corev1.PodSpec{
					Containers:         containers,
					ServiceAccountName: "local-storage-admin",
					Volumes:            volumes,
				},
			},
		},
	}
}

func diskMakerLabels(crName string) map[string]string {
	return map[string]string{
		"app":                fmt.Sprintf("local-volume-diskmaker-%s", crName),
		"openshift-operator": fmt.Sprintf("local-operator-%s", crName),
	}
}

func provisionerLabels(crName string) map[string]string {
	return map[string]string{
		"app":                fmt.Sprintf("local-volume-provisioner-%s", crName),
		"openshift-operator": fmt.Sprintf("local-operator-%s", crName),
	}
}

func storageClassLabels(crName string) map[string]string {
	return map[string]string{
		"openshift-operator": fmt.Sprintf("local-operator-%s", crName),
	}
}

func newStorageClass(cr *v1alpha1.LocalStorageProvider, scName string) *storagev1.StorageClass {
	deleteReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	firstConsumerBinding := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   scName,
			Labels: storageClassLabels(cr.Name),
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		ReclaimPolicy:     &deleteReclaimPolicy,
		VolumeBindingMode: &firstConsumerBinding,
	}
	return sc
}
