package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/auth"
)

type fakeProvider struct{}

func (*fakeProvider) Start(context.Context, auth.StartRequest) (auth.StartResult, error) {
	return auth.StartResult{RedirectURL: "https://idp.example/authorize"}, nil
}
func (*fakeProvider) Finish(context.Context, auth.FinishRequest) (auth.Identity, error) {
	return auth.Identity{Issuer: "https://idp.example", Subject: "1"}, nil
}
func (*fakeProvider) MapUser(context.Context, auth.MapRequest) (auth.User, error) {
	return auth.User{}, nil
}

type fakePolicy struct{}

func (fakePolicy) AllowSignIn(context.Context, auth.SignInAttempt) (bool, error) { return true, nil }

type fakeAvail struct{ fakeProvider }

func (*fakeAvail) Available(context.Context) (string, bool) { return "", true }

var (
	_ auth.ProviderAvailability = (*fakeAvail)(nil)
	_ auth.Provider             = (*fakeProvider)(nil)
	_ auth.SignInPolicy         = fakePolicy{}
)

func reset(t *testing.T) {
	t.Helper()
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()
	fn()
}

func TestRegisterAndList(t *testing.T) {
	reset(t)
	if len(auth.Providers()) != 0 {
		t.Fatal("providers registered by default")
	}
	auth.RegisterProvider("example", "Example IdP", &fakeProvider{})
	auth.RegisterSignInPolicy(fakePolicy{})
	got := auth.Providers()
	if len(got) != 1 || got[0] != (auth.ProviderInfo{ID: "example", Name: "Example IdP"}) {
		t.Fatalf("Providers = %+v", got)
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	auth.RegisterProvider("example", "Example", &fakeProvider{})
	auth.RegisterSignInPolicy(fakePolicy{})
	var typedNil *fakeProvider
	mustPanic(t, func() { auth.RegisterProvider("example", "Again", &fakeProvider{}) })
	mustPanic(t, func() { auth.RegisterProvider("other", "Other", nil) })
	mustPanic(t, func() { auth.RegisterProvider("other", "Other", typedNil) })
	mustPanic(t, func() { auth.RegisterProvider("Bad ID", "Other", &fakeProvider{}) })
	mustPanic(t, func() { auth.RegisterProvider("other", strings.Repeat("x", 65), &fakeProvider{}) })
	mustPanic(t, func() { auth.RegisterSignInPolicy(fakePolicy{}) })
	auth.FreezeForTest()
	mustPanic(t, func() { auth.RegisterProvider("late", "Late", &fakeProvider{}) })
}

func TestRegisterPolicyNilAndFrozen(t *testing.T) {
	reset(t)
	mustPanic(t, func() { auth.RegisterSignInPolicy(nil) })
	auth.FreezeForTest()
	mustPanic(t, func() { auth.RegisterSignInPolicy(fakePolicy{}) })
}

func TestProvisioningNotAvailable(t *testing.T) {
	reset(t)
	if _, err := auth.Provisioning(); !errors.Is(err, auth.ErrNotAvailable) {
		t.Fatalf("err = %v", err)
	}
}
