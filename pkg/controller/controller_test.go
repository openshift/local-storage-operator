package controller

import (
	"fmt"
	"os"
	"testing"

	"github.com/ghodss/yaml"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeApiUpdater struct {
	latestInstance      *localv1.LocalVolume
	oldInstance         *localv1.LocalVolume
	sas                 []*corev1.ServiceAccount
	configMaps          []*corev1.ConfigMap
	roles               []*rbacv1.Role
	roleBindings        []*rbacv1.RoleBinding
	clusterRoles        []*rbacv1.ClusterRole
	clusterRoleBindings []*rbacv1.ClusterRoleBinding
	storageClasses      []*storagev1.StorageClass
	daemonSets          []*appsv1.DaemonSet
}

func (f *fakeApiUpdater) syncStatus(oldInstance, newInstance *localv1.LocalVolume) error {
	f.latestInstance = newInstance
	f.oldInstance = oldInstance
	return nil
}

func (s *fakeApiUpdater) applyServiceAccount(sa *corev1.ServiceAccount) (*corev1.ServiceAccount, bool, error) {
	s.sas = append(s.sas, sa)
	return sa, true, nil
}

func (s *fakeApiUpdater) applyConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, bool, error) {
	s.configMaps = append(s.configMaps, configmap)
	return configmap, true, nil
}

func (s *fakeApiUpdater) applyRole(role *rbacv1.Role) (*rbacv1.Role, bool, error) {
	s.roles = append(s.roles, role)
	return role, true, nil
}

func (s *fakeApiUpdater) applyRoleBinding(roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error) {
	s.roleBindings = append(s.roleBindings, roleBinding)
	return roleBinding, true, nil
}

func (s *fakeApiUpdater) applyClusterRole(clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error) {
	s.clusterRoles = append(s.clusterRoles, clusterRole)
	return clusterRole, true, nil
}

func (s *fakeApiUpdater) applyClusterRoleBinding(roleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error) {
	s.clusterRoleBindings = append(s.clusterRoleBindings, roleBinding)
	return roleBinding, true, nil
}

func (s *fakeApiUpdater) applyStorageClass(sc *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	s.storageClasses = append(s.storageClasses, sc)
	return sc, true, nil
}

func (s *fakeApiUpdater) applyDaemonSet(ds *appsv1.DaemonSet, expectedGeneration int64, forceRollout bool) (*appsv1.DaemonSet, bool, error) {
	s.daemonSets = append(s.daemonSets, ds)
	return ds, true, nil
}
func (s *fakeApiUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return &storagev1.StorageClassList{Items: []storagev1.StorageClass{}}, nil
}

func (s *fakeApiUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	fmt.Printf("Recording event : %v", eventType)
}

func TestCreateDiskMakerConfig(t *testing.T) {
	localStorageProvider := getLocalVolume()
	handler, _ := getHandler()
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
	handler, _ := getHandler()
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

func TestSyncLocalVolumeProvider(t *testing.T) {
	localStorageProvider := getLocalVolume()
	handler, apiClient := getHandler()
	diskMakerImage := "quay.io/gnufied/local-diskmaker"
	provisionerImage := "quay.io/gnufied/local-provisioner"
	os.Setenv(diskMakerImageEnv, diskMakerImage)
	os.Setenv(provisionerImageEnv, provisionerImage)
	err := handler.syncLocalVolumeProvider(localStorageProvider)
	if err != nil {
		t.Fatalf("unexpected error : %v", err)
	}
	newInstance := apiClient.latestInstance

	localVolumeConditions := newInstance.Status.Conditions
	if len(localVolumeConditions) == 0 {
		t.Fatalf("expected local volume to be available")
	}

	c := localVolumeConditions[0]

	if c.Type != operatorv1.OperatorStatusTypeAvailable || c.Status != operatorv1.ConditionTrue {
		t.Fatalf("expected available operator condition got %v", localVolumeConditions)
	}

	if c.LastTransitionTime.IsZero() {
		t.Fatalf("expect last transition time to be set")
	}

	configMaps := apiClient.configMaps
	var provisionerConfigMap *v1.ConfigMap

	for _, c := range configMaps {
		if c.Name == "local-disks-local-provisioner-configmap" {
			provisionerConfigMap = c
		}
	}

	provisionerConfigMapData := provisionerConfigMap.Data
	labelsForPV, ok := provisionerConfigMapData["labelsForPV"]
	if !ok {
		t.Fatalf("expected labels for pv got nothing")
	}

	var labelsForPVMap map[string]string
	err = yaml.Unmarshal([]byte(labelsForPV), &labelsForPVMap)
	if err != nil {
		t.Fatalf("error unmarshalling pv labels : %v", err)
	}

	crOwnerValue, ok := labelsForPVMap["local-volume-owner"]
	if crOwnerValue != "local-disks" {
		t.Fatalf("expected cr owner to be %s got %s", "local-disks", crOwnerValue)
	}

	provisionedDaemonSets := apiClient.daemonSets
	var localProvisionerDaemonSet *appsv1.DaemonSet
	var diskMakerDaemonset *appsv1.DaemonSet
	for _, ds := range provisionedDaemonSets {
		if ds.Name == "local-disks-local-provisioner" {
			localProvisionerDaemonSet = ds
		}

		if ds.Name == "local-disks-local-diskmaker" {
			diskMakerDaemonset = ds
		}
	}

	diskMakerContainerImage := diskMakerDaemonset.Spec.Template.Spec.Containers[0].Image
	provisionerContainerImage := localProvisionerDaemonSet.Spec.Template.Spec.Containers[0].Image

	if diskMakerContainerImage != diskMakerImage {
		t.Fatalf("expected image %v got %v", diskMakerImage, diskMakerContainerImage)
	}

	if provisionerContainerImage != provisionerImage {
		t.Fatalf("expected provisioner image %v got %v", provisionerImage, provisionerContainerImage)
	}
}

func getLocalVolume() *localv1.LocalVolume {
	return &localv1.LocalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-disks",
		},
		Spec: localv1.LocalVolumeSpec{
			StorageClassDevices: []localv1.StorageClassDevice{
				{
					StorageClassName: "foo",
					VolumeMode:       localv1.PersistentVolumeFilesystem,
					FSType:           "ext4",
					DevicePaths:      []string{"/dev/sda", "/dev/sbc"},
				},
			},
		},
	}
}

func getHandler() (*Handler, *fakeApiUpdater) {
	apiClient := &fakeApiUpdater{}
	handler := &Handler{
		localStorageNameSpace: "foobar",
		localDiskLocation:     "/mnt/local-storage",
		apiClient:             apiClient,
	}
	return handler, apiClient
}
