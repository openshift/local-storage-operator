package controller

import (
	"testing"

	"github.com/ghodss/yaml"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateDiskMakerConfig(t *testing.T) {
	localStorageProvider := getLocalVolume()
	handler := getHandler()
	diskMakerConfigMap, err := handler.generateDiskMakerConfig(localStorageProvider)
	if err != nil {
		t.Fatalf("error creating disk maker configmap %v", err)
	}
	_, ok := diskMakerConfigMap.Data["diskMakerConfig"]
	if !ok {
		t.Fatalf("error getting disk maker data %v", diskMakerConfigMap)
	}
}

func TestCreateProvisionerConfigMap(t *testing.T) {
	localStorageProvider := getLocalVolume()
	handler := getHandler()
	provisionerConfigMap, err := handler.generateProvisionerConfigMap(localStorageProvider)
	if err != nil {
		t.Fatalf("error creating local provisioner configmap %v", err)
	}
	yamlData, ok := provisionerConfigMap.Data["storageClassMap"]
	if !ok {
		t.Errorf("error getting yaml data for provisioner")
	}
	var diskMap localDiskData
	err = yaml.Unmarshal([]byte(yamlData), &diskMap)
	if err != nil {
		t.Errorf("error unmarshalling yaml %v", err)
	}
	if len(diskMap["foo"]) == 0 {
		t.Errorf("error getting disk configuration")
	}
}

func getLocalVolume() *v1alpha1.LocalVolume {
	return &v1alpha1.LocalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-disks",
		},
		Spec: v1alpha1.LocalVolumeSpec{
			StorageClassDevices: []v1alpha1.StorageClassDevice{
				{
					StorageClassName: "foo",
					VolumeMode:       v1alpha1.PersistentVolumeFilesystem,
					FSType:           "ext4",
					DeviceNames:      []string{"sda", "sbc"},
				},
			},
		},
	}
}

func getHandler() *Handler {
	return &Handler{
		localStorageNameSpace: "foobar",
		localDiskLocation:     "/mnt/local-storage",
	}
}
