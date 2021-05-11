package common

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/types"
)

func TestStorageClassMap(t *testing.T) {
	m := &StorageClassOwnerMap{}
	values := map[string][]types.NamespacedName{
		"fast": []types.NamespacedName{
			{Name: "fastdisks", Namespace: "local-storage"},
			{Name: "fastworkerdisks", Namespace: "local-storage"},
		},
		"slow": []types.NamespacedName{
			{Name: "slowdisks", Namespace: "local-storage"},
		},
		"large": []types.NamespacedName{
			{Name: "largedisks", Namespace: "local-storage"},
		},
		"small": []types.NamespacedName{
			{Name: "smallerthanthirty", Namespace: "local-storage"},
			{Name: "smallerthanfifty", Namespace: "local-storage-two"},
		},
	}

	removeValues := map[string][]types.NamespacedName{
		"fast": []types.NamespacedName{
			{Name: "fastdisks", Namespace: "local-storage"},
		},
		"small": []types.NamespacedName{
			{Name: "smallerthanthirty", Namespace: "local-storage"},
		},
	}

	// register values
	for storageClass, lvSets := range values {
		for _, lvSet := range lvSets {
			m.RegisterStorageClassOwner(storageClass, lvSet)
		}
	}

	// assert they are found
	t.Log("asserting registered associations are found")
	for storageClass, lvSets := range values {
		foundLVSets := m.GetStorageClassOwners(storageClass)
		for _, lvSet := range lvSets {
			found := false
			for _, foundLVSet := range foundLVSets {
				if lvSet == foundLVSet {
					found = true
					break
				}
			}
			assert.True(t, found, "expected to find association from storageClass %q to NamespacedName: %q", storageClass, lvSets)
		}
	}

	// deregister some values
	for storageClass, lvSets := range removeValues {
		for _, lvSet := range lvSets {
			m.DeregisterStorageClassOwner(storageClass, lvSet)
		}
	}

	// assert they are not found
	t.Log("asserting deregistered associations are not found")
	for storageClass, lvSets := range removeValues {
		foundLVSets := m.GetStorageClassOwners(storageClass)
		for _, lvSet := range lvSets {
			found := false
			for _, foundLVSet := range foundLVSets {
				if lvSet == foundLVSet {
					found = true
					break
				}
			}
			assert.False(t, found, "expected not to find association from storageClass %q to NamespacedName: %q", storageClass, lvSets)
		}
	}
}
