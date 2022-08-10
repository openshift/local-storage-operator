package lvset

import (
	"sync"
	"time"
)

var (
	// deviceMinAge is the minimum age for a device to be considered safe to claim
	// otherwise, it could be a device that some other entity has attached and we have not claimed.
	deviceMinAge = time.Minute
)

// timeInterface exists so as it can be patched for testing purpose
type timeInterface interface {
	getCurrentTime() time.Time
}

type WallTime struct{}

func (t *WallTime) getCurrentTime() time.Time {
	return time.Now()
}

type ageMap struct {
	ageMap map[string]time.Time
	mux    sync.RWMutex
	clock  timeInterface
}

func newAgeMap(clock timeInterface) *ageMap {
	return &ageMap{
		clock:  clock,
		ageMap: map[string]time.Time{},
	}
}

// checks if older than,
// records current time if this is the first observation of key
func (a *ageMap) isOlderThan(key string) bool {
	a.mux.RLock()
	defer a.mux.RUnlock()

	firstObserved, found := a.ageMap[key]
	if !found {
		return false
	}
	return a.clock.getCurrentTime().Sub(firstObserved) > deviceMinAge
}

func (a *ageMap) storeDeviceAge(key string) {
	a.mux.Lock()
	defer a.mux.Unlock()

	firstObserved, found := a.ageMap[key]
	// set firstObserved if it doesn't exist
	if !found {
		firstObserved = a.clock.getCurrentTime()
		a.ageMap[key] = firstObserved
	}
}
