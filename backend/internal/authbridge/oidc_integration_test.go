//go:build integration

package authbridge

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/oidcsignin"
	"hatamoto.dev/core/internal/oidcsignin/oidctest"
)

const oidcCallback = "https://site.example/admin/login/provider/oidc/callback"

// staticSettings serves one fixed settings row.
type staticSettings oidcsignin.Settings

func (s staticSettings) Get(context.Context) (oidcsignin.Settings, bool, error) {
	return oidcsignin.Settings(s), true, nil
}

// oidcRig is the bridge in front of the real OIDC provider and a fake IdP.
type oidcRig struct {
	*bridgeRig
	idp    *oidctest.Server
	bridge *SignIn
	svc    *auth.Service
	email  string
}

// newOIDCRig registers a fresh provider (no shared cache between cases). The
// fake IdP signs in email with a new subject unless sub is set.
func newOIDCRig(t *testing.T, mod func(*oidcsignin.Settings), claims func(map[string]any)) *oidcRig {
	t.Helper()
	r := newBridgeRig(t)
	idp := oidctest.New(t, "client-1", "secret-1")
	email := r.email()
	sub := "sub-" + uuid.NewString()
	idp.Claims = func(c map[string]any) map[string]any {
		c["email"], c["sub"] = email, sub
		if claims != nil {
			claims(c)
		}
		return c
	}
	key := oidcsignin.SecretKey("test-server-secret")
	sealed, err := oidcsignin.SealSecret(key, "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	set := oidcsignin.Settings{
		Enabled: true, Issuer: idp.URL, ClientID: "client-1", SecretSealed: sealed,
		AllowedDomains: []string{email[strings.LastIndex(email, "@")+1:]}, DefaultRole: "editor",
	}
	if mod != nil {
		mod(&set)
	}
	p := &oidcsignin.Provider{}
	p.SetRuntime(oidcsignin.Runtime{Settings: staticSettings(set), SecretKey: key, Client: idp.Client()})
	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterProvider("oidc", "SSO", p)
	return &oidcRig{
		bridgeRig: r, idp: idp, email: email,
		bridge: NewSignIn(r.prov, r.idents),
		svc:    auth.NewService(r.users, nil, r.sessions),
	}
}

// signIn runs one full round: Start, the IdP's authorize redirect, Finish.
func (o *oidcRig) signIn(t *testing.T) (uuid.UUID, bool, error) {
	t.Helper()
	const state = "state-1"
	redirect, data, err := o.bridge.Start(o.ctx, "oidc", oidcCallback, state)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	c := *o.idp.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(redirect)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Get("state") != state {
		t.Fatalf("state %q came back, want %q", q.Get("state"), state)
	}
	return o.bridge.Finish(o.ctx, "oidc", authstate.FinishRequest{CallbackURL: oidcCallback, State: state, Data: data, Query: q})
}

func (o *oidcRig) users(t *testing.T) int {
	t.Helper()
	return o.count(t, `SELECT count(*) FROM hatamoto_users WHERE lower(email) = lower($1)`, o.email)
}

func (o *oidcRig) identities(t *testing.T) int {
	t.Helper()
	return o.count(t, `SELECT count(*) FROM hatamoto_user_identities i JOIN hatamoto_users u ON u.id = i.user_id WHERE lower(u.email) = lower($1)`, o.email)
}

func TestOIDCSignInEndToEnd(t *testing.T) {
	o := newOIDCRig(t, nil, nil)
	via := authstate.Via{Method: authstate.MethodProvider, Provider: "oidc"}

	uid, mfa, err := o.signIn(t)
	if err != nil || mfa {
		t.Fatalf("first sign-in = %v, mfa %v, %v", uid, mfa, err)
	}
	o.cleanupUser(t, uid)
	if o.users(t) != 1 || o.identities(t) != 1 {
		t.Fatalf("%d users, %d identities, want 1 and 1", o.users(t), o.identities(t))
	}
	if n := o.count(t, `SELECT count(*) FROM hatamoto_user_identities WHERE user_id = $1 AND issuer = $2`, uid, o.idp.URL); n != 1 {
		t.Fatalf("%d identity rows for the issuer, want 1", n)
	}
	if _, err := o.svc.IssueVerifiedSession(o.ctx, uid, via, "ua", ""); err != nil {
		t.Fatal(err)
	}
	if n := o.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind = 'admin'`, uid); n != 1 {
		t.Fatalf("%d admin sessions, want 1", n)
	}

	again, _, err := o.signIn(t)
	if err != nil || again != uid {
		t.Fatalf("second sign-in = %v, %v; want %v", again, err, uid)
	}
	if o.users(t) != 1 || o.identities(t) != 1 {
		t.Fatalf("after the second sign-in: %d users, %d identities, want 1 and 1", o.users(t), o.identities(t))
	}
}

func TestOIDCSignInRefusals(t *testing.T) {
	t.Run("unverified email is not created", func(t *testing.T) {
		o := newOIDCRig(t, nil, func(c map[string]any) { c["email_verified"] = false })
		if _, _, err := o.signIn(t); err == nil {
			t.Fatal("an unverified email signed in")
		}
		if o.users(t) != 0 {
			t.Fatal("a user was created")
		}
	})
	t.Run("unverified email is not linked", func(t *testing.T) {
		o := newOIDCRig(t, func(s *oidcsignin.Settings) { s.LinkExisting = true }, func(c map[string]any) { c["email_verified"] = false })
		existing := o.newUserWithEmail(t, o.email)
		if _, _, err := o.signIn(t); err == nil {
			t.Fatal("an unverified email was linked")
		}
		if o.identities(t) != 0 {
			t.Fatalf("identity linked to %v", existing)
		}
	})
	t.Run("other domain is not created", func(t *testing.T) {
		o := newOIDCRig(t, func(s *oidcsignin.Settings) { s.AllowedDomains = []string{"allowed.example"} }, nil)
		if _, _, err := o.signIn(t); err == nil {
			t.Fatal("a person outside the allowed domains signed in")
		}
		if o.users(t) != 0 {
			t.Fatal("a user was created")
		}
	})
	t.Run("other domain is not linked", func(t *testing.T) {
		o := newOIDCRig(t, func(s *oidcsignin.Settings) { s.AllowedDomains = []string{"allowed.example"}; s.LinkExisting = true }, nil)
		o.newUserWithEmail(t, o.email)
		if _, _, err := o.signIn(t); err == nil {
			t.Fatal("a person outside the allowed domains was linked")
		}
		if o.identities(t) != 0 {
			t.Fatal("an identity was linked")
		}
	})
	t.Run("existing account without the link switch", func(t *testing.T) {
		o := newOIDCRig(t, func(s *oidcsignin.Settings) { s.LinkExisting = false }, nil)
		o.newUserWithEmail(t, o.email)
		if _, _, err := o.signIn(t); err == nil {
			t.Fatal("an existing account was taken over with the switch off")
		}
		if o.identities(t) != 0 || o.users(t) != 1 {
			t.Fatalf("%d identities, %d users; want 0 and 1", o.identities(t), o.users(t))
		}
	})
	t.Run("existing account with the link switch", func(t *testing.T) {
		o := newOIDCRig(t, func(s *oidcsignin.Settings) { s.LinkExisting = true }, nil)
		want := o.newUserWithEmail(t, o.email)
		got, _, err := o.signIn(t)
		if err != nil || got != want {
			t.Fatalf("link = %v, %v; want %v", got, err, want)
		}
		if o.identities(t) != 1 || o.users(t) != 1 {
			t.Fatalf("%d identities, %d users; want 1 and 1", o.identities(t), o.users(t))
		}
	})
}

// newUserWithEmail creates a password user with a chosen email.
func (r *bridgeRig) newUserWithEmail(t *testing.T, email string) uuid.UUID {
	t.Helper()
	u := r.newUser(t, "editor")
	if _, err := r.db.Pool().Exec(database.WithSuperadmin(r.ctx), `UPDATE hatamoto_users SET email = $2 WHERE id = $1`, u.ID, email); err != nil {
		t.Fatal(err)
	}
	return u.ID
}
