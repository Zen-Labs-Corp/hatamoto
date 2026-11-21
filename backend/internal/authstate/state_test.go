package authstate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/authstate"
)

type fakeProvider struct{}

func (*fakeProvider) Start(context.Context, authstate.StartRequest) (authstate.StartResult, error) {
	return authstate.StartResult{}, nil
}
func (*fakeProvider) Finish(context.Context, authstate.FinishRequest) (authstate.Identity, error) {
	return authstate.Identity{}, nil
}
func (*fakeProvider) MapUser(context.Context, authstate.MapRequest) (authstate.User, error) {
	return authstate.User{}, nil
}

type fakePolicy struct {
	ok  bool
	err error
}

func (p *fakePolicy) AllowSignIn(context.Context, authstate.SignInAttempt) (bool, error) {
	return p.ok, p.err
}

type fakeProvisioner struct{ authstate.Provisioner }

func reset(t *testing.T) {
	t.Helper()
	authstate.Reset()
	t.Cleanup(authstate.Reset)
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want %q", want)
		}
		if msg, _ := r.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic %v, want %q", r, want)
		}
	}()
	fn()
}

func TestRegisterProviderPanics(t *testing.T) {
	reset(t)
	authstate.RegisterProvider("one", "One", &fakeProvider{})
	var typedNil *fakeProvider
	for name, tc := range map[string]struct {
		id, name string
		p        authstate.Provider
		want     string
	}{
		"duplicate":  {"one", "One", &fakeProvider{}, "twice"},
		"nil":        {"two", "Two", nil, "nil provider"},
		"typed nil":  {"two", "Two", typedNil, "nil provider"},
		"bad id":     {"Bad ID", "Two", &fakeProvider{}, "invalid provider id"},
		"empty id":   {"", "Two", &fakeProvider{}, "invalid provider id"},
		"empty name": {"two", "", &fakeProvider{}, "invalid provider name"},
		"long name":  {"two", strings.Repeat("x", 65), &fakeProvider{}, "invalid provider name"},
	} {
		t.Run(name, func(t *testing.T) {
			mustPanic(t, tc.want, func() { authstate.RegisterProvider(tc.id, tc.name, tc.p) })
		})
	}
	if n := len(authstate.Providers()); n != 1 {
		t.Fatalf("providers = %d, want 1", n)
	}
}

func TestRegisterAfterFreeze(t *testing.T) {
	reset(t)
	authstate.Freeze()
	mustPanic(t, "from an init function", func() { authstate.RegisterProvider("one", "One", &fakeProvider{}) })
	mustPanic(t, "from an init function", func() { authstate.RegisterSignInPolicy(&fakePolicy{ok: true}) })
}

func TestRegisterPolicyPanics(t *testing.T) {
	reset(t)
	var typedNil *fakePolicy
	mustPanic(t, "nil policy", func() { authstate.RegisterSignInPolicy(nil) })
	mustPanic(t, "nil policy", func() { authstate.RegisterSignInPolicy(typedNil) })
	authstate.RegisterSignInPolicy(&fakePolicy{ok: true})
	mustPanic(t, "twice", func() { authstate.RegisterSignInPolicy(&fakePolicy{ok: true}) })
}

func TestProvidersOrderAndCopy(t *testing.T) {
	reset(t)
	authstate.RegisterProvider("b", "Bee", &fakeProvider{})
	authstate.RegisterProvider("a", "Ay", &fakeProvider{})
	got := authstate.Providers()
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" || got[1].Name != "Ay" {
		t.Fatalf("Providers = %+v", got)
	}
	got[0].ID = "changed"
	if authstate.Providers()[0].ID != "b" {
		t.Error("Providers returned the internal slice")
	}
	if _, ok := authstate.LookupProvider("a"); !ok {
		t.Error("LookupProvider(a) missing")
	}
	if _, ok := authstate.LookupProvider("zz"); ok {
		t.Error("LookupProvider(zz) found")
	}
}

func TestAllow(t *testing.T) {
	reset(t)
	ctx := context.Background()
	if err := authstate.Allow(ctx, authstate.SignInAttempt{}); err != nil {
		t.Fatalf("no policy: %v", err)
	}
	authstate.RegisterSignInPolicy(&fakePolicy{ok: false})
	if err := authstate.Allow(ctx, authstate.SignInAttempt{}); !errors.Is(err, authstate.ErrRefused) {
		t.Fatalf("false: %v", err)
	}
	authstate.Reset()
	boom := errors.New("boom")
	authstate.RegisterSignInPolicy(&fakePolicy{ok: true, err: boom})
	if err := authstate.Allow(ctx, authstate.SignInAttempt{}); !errors.Is(err, authstate.ErrRefused) {
		t.Fatalf("error: %v", err)
	}
	authstate.Reset()
	authstate.RegisterSignInPolicy(&fakePolicy{ok: true})
	if err := authstate.Allow(ctx, authstate.SignInAttempt{}); err != nil {
		t.Fatalf("allow: %v", err)
	}
}

func TestProvisioner(t *testing.T) {
	reset(t)
	if _, err := authstate.Provisioning(); !errors.Is(err, authstate.ErrNotAvailable) {
		t.Fatalf("err = %v", err)
	}
	mustPanic(t, "nil provisioner", func() { authstate.SetProvisioner(nil) })
	authstate.SetProvisioner(fakeProvisioner{})
	if p, err := authstate.Provisioning(); err != nil || p == nil {
		t.Fatalf("Provisioning = %v, %v", p, err)
	}
	authstate.Reset()
	if _, err := authstate.Provisioning(); !errors.Is(err, authstate.ErrNotAvailable) {
		t.Fatalf("after Reset: %v", err)
	}
}

func TestViaRoundTrip(t *testing.T) {
	for _, v := range []authstate.Via{
		{Method: authstate.MethodPassword},
		{Method: authstate.MethodPasskey},
		{Method: authstate.MethodProvider, Provider: "example"},
	} {
		if got := authstate.ParseVia(v.String()); got != v {
			t.Errorf("ParseVia(%q) = %+v, want %+v", v.String(), got, v)
		}
	}
	for _, s := range []string{"", "bogus", "provider:"} {
		if got := authstate.ParseVia(s); got.Method != authstate.MethodPassword {
			t.Errorf("ParseVia(%q) = %+v", s, got)
		}
	}
}

type availProvider struct {
	fakeProvider
	label string
	ok    bool
}

func (p *availProvider) Available(context.Context) (string, bool) { return p.label, p.ok }

func TestAvailableProviders(t *testing.T) {
	reset(t)
	ctx := context.Background()
	authstate.RegisterProvider("plain", "Plain", &fakeProvider{})
	authstate.RegisterProvider("off", "Off", &availProvider{ok: false})
	authstate.RegisterProvider("renamed", "Renamed", &availProvider{label: "  Acme SSO ", ok: true})
	authstate.RegisterProvider("blank", "Blank", &availProvider{label: "  ", ok: true})
	authstate.RegisterProvider("long", "Long", &availProvider{label: strings.Repeat("é", 100), ok: true})

	got := authstate.AvailableProviders(ctx)
	want := []authstate.ProviderInfo{
		{ID: "plain", Name: "Plain"},
		{ID: "renamed", Name: "Acme SSO"},
		{ID: "blank", Name: "Blank"},
		{ID: "long", Name: strings.Repeat("é", 64)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %v, want %v", i, got[i], want[i])
		}
	}
	if n := len(authstate.Providers()); n != 5 {
		t.Errorf("Providers() lists %d, want 5 (unchanged behaviour)", n)
	}
}

func TestLookupAvailable(t *testing.T) {
	reset(t)
	ctx := context.Background()
	authstate.RegisterProvider("plain", "Plain", &fakeProvider{})
	authstate.RegisterProvider("off", "Off", &availProvider{ok: false})
	authstate.RegisterProvider("on", "On", &availProvider{ok: true})
	for id, want := range map[string]bool{"plain": true, "off": false, "on": true, "missing": false} {
		if _, ok := authstate.LookupAvailable(ctx, id); ok != want {
			t.Errorf("LookupAvailable(%q) = %v, want %v", id, ok, want)
		}
	}
}
