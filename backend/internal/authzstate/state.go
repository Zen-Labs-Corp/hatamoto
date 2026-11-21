// Package authzstate holds the authorizer registration that the public
// hatamoto.dev/core/authz package exposes. Only core calls Freeze and Reset;
// wrapper modules use the public package.
package authzstate

import (
	"reflect"
	"sync"
	"sync/atomic"
)

type slot struct{ v any }

var (
	mu     sync.Mutex
	frozen bool
	cur    atomic.Pointer[slot]
)

// Set installs v. It panics after Freeze, on a second call, and when v is nil
// or typed-nil.
func Set(v any) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("authz: Register called after app.Main started; register from an init function")
	case cur.Load() != nil:
		panic("authz: Register called twice")
	case isNil(v):
		panic("authz: Register with a nil authorizer")
	}
	cur.Store(&slot{v: v})
}

// Load returns the registered value, or nil when nothing is registered.
func Load() any {
	if s := cur.Load(); s != nil {
		return s.v
	}
	return nil
}

// isNil reports whether v is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

// Freeze makes any later Set panic. Core calls it first thing in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	cur.Store(nil)
}
