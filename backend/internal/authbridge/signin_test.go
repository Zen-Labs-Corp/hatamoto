package authbridge

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/authstate"
)

type offProvider struct{}

func (offProvider) Start(context.Context, authstate.StartRequest) (authstate.StartResult, error) {
	return authstate.StartResult{RedirectURL: "https://idp.example/auth"}, nil
}
func (offProvider) Finish(context.Context, authstate.FinishRequest) (authstate.Identity, error) {
	return authstate.Identity{Issuer: "i", Subject: "s"}, nil
}
func (offProvider) MapUser(context.Context, authstate.MapRequest) (authstate.User, error) {
	return authstate.User{}, nil
}
func (offProvider) Available(context.Context) (string, bool) { return "", false }

func TestUnavailableProviderIsUnknown(t *testing.T) {
	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterProvider("off", "Off", offProvider{})

	b := NewSignIn(nil, nil)
	ctx := context.Background()
	if got := b.Providers(ctx); len(got) != 0 {
		t.Fatalf("Providers = %v, want none", got)
	}
	if _, _, err := b.Start(ctx, "off", "https://site.example/cb", "st"); !errors.Is(err, errUnknownProvider) {
		t.Errorf("Start error = %v, want errUnknownProvider", err)
	}
	if _, _, err := b.Finish(ctx, "off", authstate.FinishRequest{}); !errors.Is(err, errUnknownProvider) {
		t.Errorf("Finish error = %v, want errUnknownProvider", err)
	}
}
