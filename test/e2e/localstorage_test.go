package e2e

import (
	"testing"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func addToScheme(s *runtime.Scheme) error {
	return localv1.AddToScheme(s)
}

func TestLocalStorageOperator(t *testing.T) {
	localVolumeList := &localv1.LocalVolumeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.SchemeGroupVersion.String(),
		},
	}
	err := framework.AddToFrameworkScheme(addToScheme, localVolumeList)
	if err != nil {
		t.Fatalf("error adding local list : %v", err)
	}
}
