package e2e

import (
	"sync"
	"testing"
)

type cleanupFn struct {
	name string
	fn   func(t *testing.T) error
}

// return a threadsafe func()[]error that has access to t and cleanupFuncs as a closure, and runs only once
// runs cleanupFuncs in reverse order
func getCleanupRunner(t *testing.T, cleanupFuncs *[]cleanupFn) func() []error {
	mux := sync.Mutex{}
	started := false
	return func() []error {
		mux.Lock()
		// lock can only be reacquired after function returns
		defer mux.Unlock()
		if !started {
			started = true
			errs := make([]error, 0)
			funcs := *cleanupFuncs
			// run in reverse
			for i := range funcs {
				f := funcs[len(funcs)-(i+1)]
				t.Logf("running %d/%d cleanup function: %s", i+1, len(funcs), f.name)
				err := f.fn(t)
				if err != nil {
					t.Logf("failed cleanup step: %+v", err)
				}
				errs = append(errs, err)
			}
			return errs
		}
		return []error{}
	}

}

func addToCleanupFuncs(cleanupFuncs *[]cleanupFn, name string, fn func(*testing.T) error) {
	*cleanupFuncs = append(*cleanupFuncs,
		cleanupFn{
			name: name,
			fn:   fn,
		},
	)
}
