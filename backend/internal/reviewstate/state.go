// Package reviewstate holds the review policy registration that the public
// hatamoto.dev/core/review package exposes. Only core calls Freeze and Reset;
// wrapper modules use the public package.
package reviewstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Default stage of a build that registers no policy and has review switched on.
const (
	DefaultStageName = "Review"
	DefaultGroup     = "reviewers"
)

// maxStages bounds what a policy may ask for.
const maxStages = 16

// Edit describes a human edit that is about to be staged.
type Edit struct {
	Requester     string
	EntityID      string
	Schema        string
	Delete        bool
	SlugChange    bool
	ReviewEnabled bool   // the site's review switch, filled in by core
	RequesterRole string // the requester's role ("owner", "editor", ...); a key's creator role
	Bypass        bool   // the requester may publish without review (core's entity.review.bypass right)
	Locale        string // the entry's locale as read before the change
	Section       string // first segment of the entry's public path without the locale prefix ("/pricing"); "" when unknown
}

// Stage is one ordered review step; Group names who may decide it.
type Stage struct {
	Name  string
	Group string
}

// Policy decides the ordered review stages of an edit. An empty result means
// the edit needs no review.
type Policy interface {
	Stages(ctx context.Context, e Edit) ([]Stage, error)
}

// ErrPolicy is returned by StagesFor when a registered policy errors or
// returns an invalid answer. The caller must hold the edit: never publish it.
var ErrPolicy = errors.New("review: policy refused the edit")

type registration struct{ p Policy }

var (
	mu     sync.Mutex
	frozen bool
	reg    atomic.Pointer[registration]
)

// Register installs the policy. It panics after Freeze, on a second call, and
// with a nil (or typed-nil) policy.
func Register(p Policy) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("review: Register called after app.Main started; register from an init function")
	case reg.Load() != nil:
		panic("review: Register called twice")
	case isNil(p):
		panic("review: Register with a nil policy")
	}
	reg.Store(&registration{p: p})
}

// isNil reports whether p is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(p any) bool {
	if p == nil {
		return true
	}
	switch v := reflect.ValueOf(p); v.Kind() {
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

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	reg.Store(nil)
	groups.Store(nil)
}

// Registered reports whether a policy is installed. With none, the switch alone
// decides, so callers may skip work only a policy needs.
func Registered() bool { return reg.Load() != nil }

// StagesFor returns the ordered review stages of e. With no policy it
// returns none when review is off or the requester holds the bypass right,
// and one "Review" stage for the "reviewers" group otherwise. A registered
// policy sees Bypass and RequesterRole and its answer wins. A policy error or invalid answer yields ErrPolicy and
// no stages. A policy panic is not recovered.
func StagesFor(ctx context.Context, e Edit) ([]Stage, error) {
	r := reg.Load()
	if r == nil {
		if !e.ReviewEnabled || e.Bypass {
			return []Stage{}, nil
		}
		return []Stage{{Name: DefaultStageName, Group: DefaultGroup}}, nil
	}
	st, err := r.p.Stages(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPolicy, err)
	}
	if len(st) > maxStages {
		return nil, fmt.Errorf("%w: %d stages", ErrPolicy, len(st))
	}
	for _, s := range st {
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Group) == "" {
			return nil, fmt.Errorf("%w: stage without name or group", ErrPolicy)
		}
	}
	if len(st) == 0 {
		return []Stage{}, nil
	}
	return slices.Clone(st), nil
}
