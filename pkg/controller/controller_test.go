package controller

import (
	"testing"

	"github.com/ghodss/yaml"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateDiskMakerConfig(t *testing.T) {
	localStorageProvider := getLocalStorageProvider()
	handler := &Handler{
		localStorageNameSpace: "foobar",
	}
	diskMakerConfigMap, err := handler.CreateDiskMakerConfig(localStorageProvider)
	if err != nil {
		t.Fatalf("error creating disk maker configmap %v", err)
	}
	_, ok := diskMakerConfigMap.Data["diskMakerConfig"]
	if !ok {
		t.Fatalf("error getting disk maker data %v", diskMakerConfigMap)
	}
}

func TestCreateProvisionerConfigMap(t *testing.T) {
	localStorageProvider := getLocalStorageProvider()
	handler := &Handler{
		localStorageNameSpace: "foobar",
	}
	provisionerConfigMap, err := handler.CreateProvisionerConfigMap(localStorageProvider)
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

func getLocalStorageProvider() *v1alpha1.LocalStorageProvider {
	return &v1alpha1.LocalStorageProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-disks",
		},
		Spec: v1alpha1.LocalStorageProviderSpec{
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
