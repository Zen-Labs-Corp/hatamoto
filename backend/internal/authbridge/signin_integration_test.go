//go:build integration

package authbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
)

// fakeProvider asserts a fixed identity and maps it with a pluggable function.
type fakeProvider struct {
	ident     authstate.Identity
	finishErr error
	mapUser   func(ctx context.Context, req authstate.MapRequest) (authstate.User, error)
}

func (f *fakeProvider) Start(context.Context, authstate.StartRequest) (authstate.StartResult, error) {
	return authstate.StartResult{RedirectURL: "https://idp.example.test/auth", Data: "d"}, nil
}

func (f *fakeProvider) Finish(context.Context, authstate.FinishRequest) (authstate.Identity, error) {
	return f.ident, f.finishErr
}

func (f *fakeProvider) MapUser(ctx context.Context, req authstate.MapRequest) (authstate.User, error) {
	return f.mapUser(ctx, req)
}

// registerFake registers p as "fake" for one test.
func registerFake(t *testing.T, p authstate.Provider) {
	t.Helper()
	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterProvider("fake", "Fake", p)
}

// provisioning maps an identity the way a provider module would: link by
// subject, else create by email and link.
func provisioning(ctx context.Context, req authstate.MapRequest) (authstate.User, error) {
	if req.Linked != nil {
		return *req.Linked, nil
	}
	u, _, err := req.Users.FindOrCreate(ctx, authstate.NewUser{Email: req.Identity.Email, Name: req.Identity.Name, Role: "editor"})
	if err != nil {
		return authstate.User{}, err
	}
	return u, req.Users.LinkIdentity(ctx, u.ID, req.Identity.Issuer, req.Identity.Subject)
}

func TestSignInProviderEndToEnd(t *testing.T) {
	r := newBridgeRig(t)
	ident := authstate.Identity{Issuer: "https://idp.example.test", Subject: uuid.NewString(), Email: r.email(), Name: "Ana", MFA: true}
	registerFake(t, &fakeProvider{ident: ident, mapUser: provisioning})
	bridge := NewSignIn(r.prov, r.idents)
	svc := auth.NewService(r.users, nil, r.sessions)
	via := authstate.Via{Method: authstate.MethodProvider, Provider: "fake"}

	uid, mfa, err := bridge.Finish(r.ctx, "fake", authstate.FinishRequest{})
	if err != nil || !mfa {
		t.Fatalf("Finish = %v, %v, %v", uid, mfa, err)
	}
	r.cleanupUser(t, uid)
	if _, err := svc.IssueVerifiedSession(r.ctx, uid, via, "ua", ""); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind = 'admin'`, uid); n != 1 {
		t.Fatalf("%d admin sessions, want 1", n)
	}

	// The second sign-in finds the linked user; no duplicate is made.
	again, _, err := bridge.Finish(r.ctx, "fake", authstate.FinishRequest{})
	if err != nil || again != uid {
		t.Fatalf("second Finish = %v, %v", again, err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_users WHERE lower(email) = lower($1)`, ident.Email); n != 1 {
		t.Fatalf("%d users for the email, want 1", n)
	}

	// Disable ends access at once: the next sign-in is refused, no new session.
	if err := r.prov.Disable(r.ctx, uid.String()); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, uid); n != 0 {
		t.Fatalf("%d sessions after Disable", n)
	}
	uid, _, err = bridge.Finish(r.ctx, "fake", authstate.FinishRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IssueVerifiedSession(r.ctx, uid, via, "ua", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("disabled user: %v, want ErrInvalidCredentials", err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind = 'admin'`, uid); n != 0 {
		t.Fatalf("%d sessions after the refusal", n)
	}
}

func TestSignInRefusesAnUnlinkedMapping(t *testing.T) {
	r := newBridgeRig(t)
	existing := r.newUser(t, "editor")
	ident := authstate.Identity{Issuer: "https://idp.example.test", Subject: uuid.NewString(), Email: existing.Email}
	registerFake(t, &fakeProvider{ident: ident, mapUser: func(ctx context.Context, req authstate.MapRequest) (authstate.User, error) {
		// A careless provider: returns the user with that email without linking.
		return req.Users.FindByEmail(ctx, req.Identity.Email)
	}})

	if _, _, err := NewSignIn(r.prov, r.idents).Finish(r.ctx, "fake", authstate.FinishRequest{}); err == nil {
		t.Fatal("Finish succeeded for a user that is not linked")
	}
	if _, err := r.prov.FindByIdentity(r.ctx, ident.Issuer, ident.Subject); !errors.Is(err, authstate.ErrNotFound) {
		t.Fatalf("an identity was linked by core: %v", err)
	}
}

func TestSignInProviderErrors(t *testing.T) {
	r := newBridgeRig(t)
	boom := errors.New("idp down")
	registerFake(t, &fakeProvider{finishErr: boom, mapUser: provisioning})
	bridge := NewSignIn(r.prov, r.idents)
	if _, _, err := bridge.Finish(r.ctx, "fake", authstate.FinishRequest{}); !errors.Is(err, boom) {
		t.Fatalf("Finish error = %v, want the provider's", err)
	}
	if _, _, err := bridge.Finish(r.ctx, "other", authstate.FinishRequest{}); err == nil {
		t.Fatal("unknown provider succeeded")
	}

	registerFake(t, &fakeProvider{ident: authstate.Identity{Issuer: "i"}, mapUser: provisioning})
	if _, _, err := bridge.Finish(r.ctx, "fake", authstate.FinishRequest{}); err == nil {
		t.Fatal("identity without a subject succeeded")
	}

	mapErr := errors.New("mapping failed")
	registerFake(t, &fakeProvider{ident: authstate.Identity{Issuer: "i", Subject: "s"}, mapUser: func(context.Context, authstate.MapRequest) (authstate.User, error) {
		return authstate.User{}, mapErr
	}})
	if _, _, err := bridge.Finish(r.ctx, "fake", authstate.FinishRequest{}); !errors.Is(err, mapErr) {
		t.Fatalf("MapUser error = %v, want it surfaced", err)
	}

	res, data, err := func() (string, string, error) {
		registerFake(t, &fakeProvider{})
		return bridge.Start(r.ctx, "fake", "https://site.example.test/cb", "st")
	}()
	if err != nil || res == "" || data != "d" {
		t.Fatalf("Start = %q, %q, %v", res, data, err)
	}
}
