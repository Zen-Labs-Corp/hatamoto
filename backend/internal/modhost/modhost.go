// Package modhost holds the registrations behind the public
// hatamoto.dev/core/module/... packages. Only core calls Freeze, the Set*
// source functions and Reset; modules register from init functions through
// the public packages.
package modhost

import (
	"fmt"
	"reflect"
	"regexp"
	"sync"
)

var (
	mu     sync.Mutex
	frozen bool
	resets []func()
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// ValidName reports whether s is an acceptable module name.
func ValidName(s string) bool { return validName.MatchString(s) }

// Freeze makes any later registration panic. Core calls it in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Reset reopens registration and clears every registry; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	for _, fn := range resets {
		fn()
	}
}

// onReset records a registry reset function. Hook files call it from init.
// The functions run with mu held.
func onReset(fn func()) { resets = append(resets, fn) }

// register runs fn with the registry lock held, panicking first when the
// registry is frozen.
func register(what string, fn func()) {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic(fmt.Sprintf("%s: called after app.Main started; register from an init function", what))
	}
	fn()
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
