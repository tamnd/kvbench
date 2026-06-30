package engine

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a fresh, unopened Engine for one run.
type Factory func() Engine

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register makes an adapter available by name. Adapters call this from an
// init() function. This is the only coupling point between the harness and a
// concrete engine: the harness core never imports an adapter package.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("kvbench: duplicate engine registration: " + name)
	}
	registry[name] = f
}

// New builds the named engine, or errors if it is not compiled into this binary.
func New(name string) (Engine, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("engine %q not built into this binary (try a build tag: cgo_engines, rust_engines)", name)
	}
	return f(), nil
}

// Names lists every engine compiled into this binary, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DefaultNames lists the engines that make up the default sweep and the
// published board: every compiled-in engine except the reference rails (the
// in-memory ceilings, the devnull floor, and the bare kv cores). Reference
// engines stay runnable when named explicitly; they just never stand in a
// table next to a real store.
func DefaultNames() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n, f := range registry {
		if f().Meta().Reference {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Has reports whether an engine is available in this binary.
func Has(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registry[name]
	return ok
}
