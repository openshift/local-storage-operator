package common

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// StorageClassOwnerMap
// store a one to many association from storageClass to storageclass owner (LocalVolume,LocalVolumeSet,etc),
// so that one PV/SC event can fan out requests to all owners.
type StorageClassOwnerMap struct {
	storageClassMap map[string]map[types.NamespacedName]struct{}
	mux             sync.Mutex
}

func (l *StorageClassOwnerMap) GetStorageClassOwners(storageClass string) []types.NamespacedName {
	l.mux.Lock()
	defer l.mux.Unlock()
	if len(l.storageClassMap) < 1 {
		return make([]types.NamespacedName, 0)
	}
	names, found := l.storageClassMap[storageClass]
	if !found {
		return make([]types.NamespacedName, 0)
	}
	if len(names) < 1 {
		return make([]types.NamespacedName, 0)
	}
	result := make([]types.NamespacedName, 0)
	for name := range names {
		result = append(result, name)
	}
	return result
}

func (l *StorageClassOwnerMap) RegisterStorageClassOwner(storageClass string, name types.NamespacedName) {
	l.mux.Lock()
	defer l.mux.Unlock()
	if len(l.storageClassMap) < 1 {
		l.storageClassMap = make(map[string]map[types.NamespacedName]struct{})
	}
	names, found := l.storageClassMap[storageClass]
	if !found {
		l.storageClassMap[storageClass] = map[types.NamespacedName]struct{}{name: {}}
	} else if len(names) < 1 {
		l.storageClassMap[storageClass] = map[types.NamespacedName]struct{}{name: {}}
	} else {
		l.storageClassMap[storageClass][name] = struct{}{}
	}
	return
}

func (l *StorageClassOwnerMap) DeregisterStorageClassOwner(storageClass string, name types.NamespacedName) {
	l.mux.Lock()
	defer l.mux.Unlock()
	if len(l.storageClassMap) < 1 {
		l.storageClassMap = make(map[string]map[types.NamespacedName]struct{})
	}
	names, found := l.storageClassMap[storageClass]
	if !found {
		return
	} else if len(names) < 1 {
		return
	} else {
		delete(names, name)
	}
	return
}
