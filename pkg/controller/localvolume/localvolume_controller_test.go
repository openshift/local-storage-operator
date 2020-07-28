package localvolume

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/ghodss/yaml"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type daemonSetRollout struct {
	daemonSet    *appsv1.DaemonSet
	forceRollout bool
}

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
	daemonSets          []daemonSetRollout
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
	dsRollout := daemonSetRollout{forceRollout: forceRollout, daemonSet: ds}
	s.daemonSets = append(s.daemonSets, dsRollout)
	return ds, true, nil
}

func (s *fakeApiUpdater) getDaemonSet(namespace, dsName string) (*appsv1.DaemonSet, error) {
	localVolume := getLocalVolume()
	handler, _ := getHandler()
	ds := handler.generateDiskMakerDaemonSet(localVolume)
	return ds, nil
}

func (s *fakeApiUpdater) listStorageClasses(listOptions metav1.ListOptions) (*storagev1.StorageClassList, error) {
	return &storagev1.StorageClassList{Items: []storagev1.StorageClass{}}, nil
}

func (s *fakeApiUpdater) listPersistentVolumes(listOptions metav1.ListOptions) (*corev1.PersistentVolumeList, error) {
	return &corev1.PersistentVolumeList{Items: []corev1.PersistentVolume{}}, nil
}

func (s *fakeApiUpdater) recordEvent(lv *localv1.LocalVolume, eventType, reason, messageFmt string, args ...interface{}) {
	fmt.Printf("Recording event : %v", eventType)
}

func (s *fakeApiUpdater) updateLocalVolume(lv *localv1.LocalVolume) error {
	s.latestInstance = lv
	return nil
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
	os.Setenv(common.DiskMakerImageEnv, diskMakerImage)
	os.Setenv(common.ProvisionerImageEnv, provisionerImage)
	err := handler.syncLocalVolumeProvider(localStorageProvider)
	if err != nil {
		t.Fatalf("unexpected error : %v", err)
	}
	newInstance := apiClient.latestInstance

	if len(newInstance.GetFinalizers()) == 0 {
		t.Fatalf("expected local volume to have finalizers")
	}

	// rerun the sync again so as rest of the code can run
	err = handler.syncLocalVolumeProvider(newInstance)
	if err != nil {
		t.Fatalf("unexpected error while syncing localvolume: %v", err)
	}

	newInstance = apiClient.latestInstance

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
	var diskMakeConfigMap *v1.ConfigMap

	for _, c := range configMaps {
		if c.Name == "local-disks-local-provisioner-configmap" {
			provisionerConfigMap = c
		}

		if c.Name == "local-disks-diskmaker-configmap" {
			diskMakeConfigMap = c
		}
	}

	err = verifyDiskMakerConfigmap(diskMakeConfigMap, localStorageProvider)

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

	crOwnerValue, ok := labelsForPVMap[commontypes.LocalVolumeOwnerNameForPV]
	if crOwnerValue != "local-disks" {
		t.Fatalf("expected cr owner to be %s got %s", "local-disks", crOwnerValue)
	}

	provisionedDaemonSets := apiClient.daemonSets
	var localProvisionerDaemonSet *appsv1.DaemonSet
	var diskMakerDaemonset *appsv1.DaemonSet
	for _, ds := range provisionedDaemonSets {
		if ds.daemonSet.Name == "local-disks-local-provisioner" && ds.forceRollout {
			localProvisionerDaemonSet = ds.daemonSet
		}

		if ds.daemonSet.Name == "local-disks-local-diskmaker" && ds.forceRollout {
			diskMakerDaemonset = ds.daemonSet
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

func TestTolerationProvisionerDS(t *testing.T) {
	localStorageProvider := getLocalVolumeWithTolerations()
	handler, _ := getHandler()
	provisionerDS := handler.generateLocalProvisionerDaemonset(localStorageProvider)
	if provisionerDS == nil {
		t.Fatalf("error creating local provisioner DaemonSet")
	}
	data := provisionerDS.Spec.Template.Spec.Tolerations
	if data == nil || len(data) == 0 {
		t.Errorf("error getting toleration data from provisioner DaemonSet")
	}
	toleration := data[0]
	if toleration.Key != "localstorage" {
		t.Errorf("key mismatch %v", toleration.Key)
	}
	if toleration.Value != "true" {
		t.Errorf("value mismatch %v", toleration.Value)
	}
}

func TestTolerationDiskmakerDS(t *testing.T) {
	localStorageProvider := getLocalVolumeWithTolerations()
	handler, _ := getHandler()
	diskmakerDS := handler.generateDiskMakerDaemonSet(localStorageProvider)
	if diskmakerDS == nil {
		t.Fatalf("error creating diskmaker DaemonSet")
	}
	data := diskmakerDS.Spec.Template.Spec.Tolerations
	if data == nil || len(data) == 0 {
		t.Errorf("error getting toleration data from diskmaker DaemonSet")
	}
	toleration := data[0]
	if toleration.Key != "localstorage" {
		t.Errorf("key mismatch %v", toleration.Key)
	}
	if toleration.Value != "true" {
		t.Errorf("value mismatch %v", toleration.Value)
	}
}

func TestCheckDaemonSetHash(t *testing.T) {
	tests := []struct {
		name            string
		forceRollout    bool
		updateDaemonSet bool
		expectUpdate    bool
	}{
		{
			name:            "DaemonSet is not updated and forceRollout is false, do not update",
			forceRollout:    false,
			updateDaemonSet: false,
			expectUpdate:    false,
		},
		{
			name:            "DaemonSet is not updated and forceRollout is true, update",
			forceRollout:    true,
			updateDaemonSet: false,
			expectUpdate:    true,
		},
		{
			name:            "DaemonSet is updated and forceRollout is false, update",
			forceRollout:    false,
			updateDaemonSet: true,
			expectUpdate:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			localStorageProvider := getLocalVolume()
			handler, _ := getHandler()
			diskMakerDS := handler.generateDiskMakerDaemonSet(localStorageProvider)
			origAnnotations := diskMakerDS.ObjectMeta.Annotations[specHashAnnotation]

			if test.updateDaemonSet {
				// The default DS has a Hash included. We want to change the spec
				// and ensure that it's updated.
				diskMakerDS.Spec.MinReadySeconds = 300
				err := addDaemonSetHash(diskMakerDS)
				if err != nil {
					t.Errorf("error attempting to add hash to existing DS: %v", err)
				}
			}

			needsUpdate := handler.checkDaemonSetHash(diskMakerDS, test.forceRollout)

			if test.expectUpdate != needsUpdate {
				t.Errorf("Expected update does not match update value: %v", needsUpdate)
			}

			if test.updateDaemonSet {
				if reflect.DeepEqual(origAnnotations, diskMakerDS.ObjectMeta.Annotations[specHashAnnotation]) {
					t.Errorf("DaemonSet annotations are identical after update")
				}
			}
		})
	}
}

func verifyDiskMakerConfigmap(configMap *v1.ConfigMap, lv *localv1.LocalVolume) error {
	makerData, ok := configMap.Data["diskMakerConfig"]
	if !ok {
		return fmt.Errorf("error getting diskmaker data")
	}

	diskMakerConfig := &diskmaker.DiskConfig{}
	err := yaml.Unmarshal([]byte(makerData), diskMakerConfig)
	if err != nil {
		return fmt.Errorf("error unmarshalling the configmap %v", err)
	}

	disks := diskMakerConfig.Disks
	if len(disks) != len(lv.Spec.StorageClassDevices) {
		return fmt.Errorf("expected %d devices got %d", len(lv.Spec.StorageClassDevices), len(disks))
	}

	if diskMakerConfig.OwnerName != lv.Name {
		return fmt.Errorf("expected owner to be %s got %s", lv.Name, diskMakerConfig.OwnerName)
	}
	return nil
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

func getLocalVolumeWithTolerations() *localv1.LocalVolume {
	lv := getLocalVolume()
	lv.Spec.Tolerations = []corev1.Toleration{
		{
			Key:      "localstorage",
			Operator: "Equal",
			Value:    "true",
		},
	}
	return lv
}

func getHandler() (*ReconcileLocalVolume, *fakeApiUpdater) {
	apiClient := &fakeApiUpdater{}
	handler := &ReconcileLocalVolume{
		localStorageNameSpace: "foobar",
		localDiskLocation:     "/mnt/local-storage",
		apiClient:             apiClient,
	}
	return handler, apiClient
}
