// Package authstate holds the sign-in provider registry, the sign-in policy
// and the provisioner that the public hatamoto.dev/core/auth package exposes.
// Only core calls Freeze, SetProvisioner and Reset; wrapper modules use the
// public package.
package authstate

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

type registered struct {
	info ProviderInfo
	p    Provider
}

var (
	mu          sync.Mutex
	frozen      bool
	providers   []registered
	policy      SignInPolicy
	provisioner Provisioner
	validID     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// RegisterProvider adds a provider. It panics after Freeze, on a duplicate or
// invalid id, an empty or over-long name, or a nil (or typed-nil) provider.
func RegisterProvider(id, name string, p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic("auth: RegisterProvider called after app.Main started; register from an init function")
	}
	if isNil(p) {
		panic("auth: RegisterProvider with a nil provider")
	}
	if !validID.MatchString(id) {
		panic(fmt.Sprintf("auth: invalid provider id %q", id))
	}
	if n := utf8.RuneCountInString(name); name == "" || n > 64 {
		panic(fmt.Sprintf("auth: invalid provider name for %q: want 1-64 characters", id))
	}
	for _, r := range providers {
		if r.info.ID == id {
			panic(fmt.Sprintf("auth: RegisterProvider called twice for %q", id))
		}
	}
	providers = append(providers, registered{info: ProviderInfo{ID: id, Name: name}, p: p})
}

// RegisterSignInPolicy installs the sign-in policy. It panics after Freeze,
// on a second call, or with a nil (or typed-nil) policy.
func RegisterSignInPolicy(p SignInPolicy) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("auth: RegisterSignInPolicy called after app.Main started; register from an init function")
	case policy != nil:
		panic("auth: RegisterSignInPolicy called twice")
	case isNil(p):
		panic("auth: RegisterSignInPolicy with a nil policy")
	}
	policy = p
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

// Providers lists the registered providers in registration order.
func Providers() []ProviderInfo {
	mu.Lock()
	defer mu.Unlock()
	out := make([]ProviderInfo, len(providers))
	for i, r := range providers {
		out[i] = r.info
	}
	return out
}

// LookupProvider returns the provider registered under id.
func LookupProvider(id string) (Provider, bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range providers {
		if r.info.ID == id {
			return r.p, true
		}
	}
	return nil, false
}

// AvailableProviders lists the registered providers that are available now,
// with their current label. Providers without ProviderAvailability are always
// available.
func AvailableProviders(ctx context.Context) []ProviderInfo {
	mu.Lock()
	regs := slices.Clone(providers)
	mu.Unlock() // never call a provider under mu: Available may read the database
	out := make([]ProviderInfo, 0, len(regs))
	for _, r := range regs {
		if info, ok := availability(ctx, r); ok {
			out = append(out, info)
		}
	}
	return out
}

// LookupAvailable is LookupProvider that also requires the provider to be
// available.
func LookupAvailable(ctx context.Context, id string) (Provider, bool) {
	p, ok := LookupProvider(id)
	if !ok {
		return nil, false
	}
	mu.Lock()
	var reg registered
	for _, r := range providers {
		if r.info.ID == id {
			reg = r
			break
		}
	}
	mu.Unlock()
	if _, ok := availability(ctx, reg); !ok {
		return nil, false
	}
	return p, true
}

// availability resolves the current label of r; ok is false when the provider
// reports itself unavailable. A blank or invalid label keeps the registered
// name and a label over 64 runes is clipped.
func availability(ctx context.Context, r registered) (ProviderInfo, bool) {
	a, isAvail := r.p.(ProviderAvailability)
	if !isAvail {
		return r.info, true
	}
	label, ok := a.Available(ctx)
	if !ok {
		return ProviderInfo{}, false
	}
	info := r.info
	if label = strings.TrimSpace(label); label != "" && utf8.ValidString(label) {
		if utf8.RuneCountInString(label) > 64 {
			label = string([]rune(label)[:64])
		}
		info.Name = label
	}
	return info, true
}

// Allow asks the policy about a sign-in. No policy allows. A refusal or an
// error from the policy yields an error wrapping ErrRefused (fail closed).
// A policy panic is not recovered.
func Allow(ctx context.Context, a SignInAttempt) error {
	mu.Lock()
	p := policy
	mu.Unlock()
	if p == nil {
		return nil
	}
	ok, err := p.AllowSignIn(ctx, a)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefused, err)
	}
	if !ok {
		return ErrRefused
	}
	return nil
}

// SetProvisioner is called by core once the database is ready (serve only).
func SetProvisioner(p Provisioner) {
	if isNil(p) {
		panic("auth: nil provisioner")
	}
	mu.Lock()
	provisioner = p
	mu.Unlock()
}

// Provisioning returns the provisioner, or ErrNotAvailable outside serve.
func Provisioning() (Provisioner, error) {
	mu.Lock()
	defer mu.Unlock()
	if provisioner == nil {
		return nil, ErrNotAvailable
	}
	return provisioner, nil
}

// Freeze makes any later Register call panic. Core calls it first thing in Main.
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
	providers = nil
	policy = nil
	provisioner = nil
}
