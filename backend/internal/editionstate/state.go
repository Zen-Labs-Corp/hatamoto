// Package editionstate holds the edition registration that the public
// hatamoto.dev/core/edition package exposes. Only core calls Freeze,
// SetSiteIDSource and Reset; wrapper modules use the public package.
package editionstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sync"
	"sync/atomic"
)

// Community is the edition name of a build that registers nothing.
const Community = "community"

// Gate decides which features and plugins an edition enables.
type Gate interface {
	FeatureEnabled(ctx context.Context, feature string) (bool, error)
	PluginEntitled(ctx context.Context, pluginID string) (bool, error)
}

// ErrNoSiteID is returned by SiteID when no source is installed.
var ErrNoSiteID = errors.New("edition: site id not available in this process")

type registration struct {
	name string
	gate Gate
}

type siteSource func(context.Context) (string, error)

var (
	mu        sync.Mutex
	frozen    bool
	reg       atomic.Pointer[registration]
	site      atomic.Pointer[siteSource]
	validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Register installs the edition name and gate. It panics after Freeze, on a
// second call, with a nil (or typed-nil) gate, or with a reserved or invalid name.
func Register(name string, g Gate) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("edition: Register called after app.Main started; register from an init function")
	case reg.Load() != nil:
		panic(fmt.Sprintf("edition: Register called twice (already %q)", reg.Load().name))
	case isNil(g):
		panic("edition: Register with a nil gate")
	case name == Community || !validName.MatchString(name):
		panic(fmt.Sprintf("edition: invalid edition name %q", name))
	}
	reg.Store(&registration{name: name, gate: g})
}

// isNil reports whether g is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(g Gate) bool {
	if g == nil {
		return true
	}
	switch v := reflect.ValueOf(g); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// Freeze makes any later Register panic. Core calls it first thing in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Name returns the registered edition name, or Community.
func Name() string {
	if r := reg.Load(); r != nil {
		return r.name
	}
	return Community
}

// Feature reports whether the gate enables feature. It fails closed: no
// gate, a false answer or an error all yield false. A gate panic is not
// recovered.
func Feature(ctx context.Context, feature string) bool {
	r := reg.Load()
	if r == nil {
		return false
	}
	ok, err := r.gate.FeatureEnabled(ctx, feature)
	return err == nil && ok
}

// Plugin reports whether the gate entitles pluginID, failing closed like Feature.
func Plugin(ctx context.Context, pluginID string) bool {
	r := reg.Load()
	if r == nil {
		return false
	}
	ok, err := r.gate.PluginEntitled(ctx, pluginID)
	return err == nil && ok
}

// SetSiteIDSource is called by core once the database is migrated (serve
// only). Core never sends the site id anywhere.
func SetSiteIDSource(fn func(context.Context) (string, error)) {
	if fn == nil {
		panic("edition: nil site id source")
	}
	s := siteSource(fn)
	site.Store(&s)
}

// SiteID returns the stable site id, or ErrNoSiteID when no source is
// installed. Core never sends the site id anywhere.
func SiteID(ctx context.Context) (string, error) {
	p := site.Load()
	if p == nil {
		return "", ErrNoSiteID
	}
	return (*p)(ctx)
}

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	reg.Store(nil)
	site.Store(nil)
}
