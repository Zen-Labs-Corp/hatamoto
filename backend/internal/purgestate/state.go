// Package purgestate holds the cache purge registration that the public
// hatamoto.dev/core/purge package exposes. Only core calls Freeze, Registered
// and Reset; wrapper modules use the public package.
package purgestate

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sync"
)

// Request names the cached copies that are stale.
type Request struct {
	URLs []string // absolute URLs whose cached copies are stale
	All  bool     // every cached page is stale (the owner cleared the cache)
}

// Purger tells an edge cache that copies are stale.
type Purger interface {
	Purge(ctx context.Context, req Request) error
}

var (
	mu        sync.Mutex
	frozen    bool
	regName   string
	regPurger Purger
	validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Register installs the purger. It panics after Freeze, on a second call,
// with a nil (or typed-nil) purger, or with an invalid name.
func Register(name string, p Purger) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("purge: Register called after app.Main started; register from an init function")
	case regPurger != nil:
		panic(fmt.Sprintf("purge: Register called twice (already %q)", regName))
	case isNil(p):
		panic("purge: Register with a nil purger")
	case !validName.MatchString(name):
		panic(fmt.Sprintf("purge: invalid purger name %q", name))
	}
	regName, regPurger = name, p
}

// isNil reports whether p is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(p Purger) bool {
	if p == nil {
		return true
	}
	switch v := reflect.ValueOf(p); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// Registered returns the registered purger, if any.
func Registered() (name string, p Purger, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	return regName, regPurger, regPurger != nil
}

// Freeze makes any later Register panic. Core calls it first thing in Main.
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
	regName, regPurger = "", nil
}
