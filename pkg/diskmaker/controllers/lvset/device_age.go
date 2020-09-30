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

type ageMap struct {
	ageMap      map[string]time.Time
	mux         sync.Mutex
	currentTime *func() time.Time
}

func newAgeMap() *ageMap {
	f := func() time.Time {
		return time.Now()
	}
	return &ageMap{currentTime: &f}
}

// checks if older than,
// records current time if this is the first observation of key
func (a *ageMap) isOlderThan(key string, t time.Duration) bool {
	firstObserved := a.getFirstObserved(key)
	currentTime := *a.currentTime
	return currentTime().Sub(firstObserved) > t
}

// getFirstObserved returns the age that the key was first observed and records it if
// it was first observed now
func (a *ageMap) getFirstObserved(key string) time.Time {
	a.mux.Lock()
	defer a.mux.Unlock()

	if len(a.ageMap) == 0 {
		a.ageMap = make(map[string]time.Time, 0)
	}

	firstObserved, found := a.ageMap[key]
	// set firstObserved if it doesn't exist
	if !found {
		getTime := *a.currentTime
		firstObserved = getTime()
		a.ageMap[key] = firstObserved
	}
	return firstObserved
}

func (a *ageMap) freezeTimeAt(t time.Time) {
	f := func() time.Time {
		return t
	}
	a.currentTime = &f
}
