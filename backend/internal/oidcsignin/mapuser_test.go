package oidcsignin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authstate"
)

// memUsers is an in-memory Provisioner; unimplemented methods panic.
type memUsers struct {
	authstate.Provisioner
	byEmail   map[string]authstate.User
	created   []authstate.NewUser
	linked    []string
	createErr error
	linkErr   error
}

func (m *memUsers) FindByEmail(_ context.Context, email string) (authstate.User, error) {
	if u, ok := m.byEmail[email]; ok {
		return u, nil
	}
	return authstate.User{}, authstate.ErrNotFound
}

func (m *memUsers) Create(_ context.Context, n authstate.NewUser) (authstate.User, error) {
	if m.createErr != nil {
		return authstate.User{}, m.createErr
	}
	m.created = append(m.created, n)
	return authstate.User{ID: "u-new", Email: n.Email, Name: n.Name, Role: n.Role}, nil
}

func (m *memUsers) LinkIdentity(_ context.Context, id, issuer, subject string) error {
	if m.linkErr != nil {
		return m.linkErr
	}
	m.linked = append(m.linked, id+"|"+issuer+"|"+subject)
	return nil
}

type mapRig struct {
	p      *Provider
	src    *fakeSource
	users  *memUsers
	events []auditstate.Event
}

func newMapRig() *mapRig {
	r := &mapRig{
		src:   &fakeSource{found: true},
		users: &memUsers{byEmail: map[string]authstate.User{"bob@acme.com": {ID: "u-bob", Email: "bob@acme.com", Role: "owner"}}},
		p:     &Provider{},
	}
	sealed, _ := SealSecret(testKey, testSecret)
	r.src.s = Settings{
		Enabled: true, Issuer: "https://idp.example.test", ClientID: testClientID, SecretSealed: sealed,
		AllowedDomains: []string{"acme.com"}, DefaultRole: "editor",
	}
	r.p.SetRuntime(Runtime{Settings: r.src, SecretKey: testKey, Audit: func(_ context.Context, ev auditstate.Event) {
		r.events = append(r.events, ev)
	}})
	return r
}

func (r *mapRig) mapUser(email string, verified bool, linked *authstate.User) (authstate.User, error) {
	return r.p.MapUser(context.Background(), authstate.MapRequest{
		Identity: authstate.Identity{Issuer: "https://idp.example.test", Subject: "sub-9", Email: email, EmailVerified: verified, Name: "Ana Perez"},
		Linked:   linked, Users: r.users,
	})
}

func wantReason(t *testing.T, err error, reason string) {
	t.Helper()
	if got := ReasonOf(err); got != reason {
		t.Fatalf("reason = %q (%v), want %q", got, err, reason)
	}
	if strings.Contains(err.Error(), "@") {
		t.Fatalf("error %q contains an email", err)
	}
}

func TestMapUserLinkedIsReturnedUntouched(t *testing.T) {
	r := newMapRig()
	r.src.s.Enabled = false // a linked identity does not consult the settings (D-5)
	linked := &authstate.User{ID: "u-1", Email: "x@elsewhere.org"}
	got, err := r.mapUser("x@elsewhere.org", false, linked)
	if err != nil || got != *linked {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(r.users.created)+len(r.users.linked)+len(r.events) != 0 {
		t.Fatal("a linked identity changed an account")
	}
}

func TestMapUserRefusals(t *testing.T) {
	for _, linkExisting := range []bool{false, true} {
		for _, email := range []string{"new@acme.com", "bob@acme.com"} {
			r := newMapRig()
			r.src.s.LinkExisting = linkExisting
			_, err := r.mapUser(email, false, nil)
			wantReason(t, err, reasonEmailUnverified)
			if len(r.users.created)+len(r.users.linked)+len(r.events) != 0 {
				t.Fatalf("unverified %s changed an account", email)
			}
		}
	}
	for _, email := range []string{"ana@other.org", "ana@a.acme.com", "ana@acme.com.evil.test", "ana", "ana@acme.com@evil.test"} {
		for _, linkExisting := range []bool{false, true} {
			r := newMapRig()
			r.src.s.LinkExisting = linkExisting
			_, err := r.mapUser(email, true, nil)
			wantReason(t, err, reasonDomainNotAllowed)
			if len(r.users.created)+len(r.users.linked) != 0 {
				t.Fatalf("%s changed an account", email)
			}
		}
	}
	// An existing account in another domain is not linked either.
	r := newMapRig()
	r.src.s.LinkExisting = true
	r.users.byEmail["eve@other.org"] = authstate.User{ID: "u-eve", Email: "eve@other.org"}
	_, err := r.mapUser("eve@other.org", true, nil)
	wantReason(t, err, reasonDomainNotAllowed)
}

func TestMapUserDisabled(t *testing.T) {
	r := newMapRig()
	r.src.s.Enabled = false
	_, err := r.mapUser("new@acme.com", true, nil)
	wantReason(t, err, reasonDisabled)
}

func TestMapUserExistingAccount(t *testing.T) {
	r := newMapRig()
	_, err := r.mapUser("bob@acme.com", true, nil)
	wantReason(t, err, reasonAccountExists)
	if len(r.users.linked) != 0 || len(r.users.created) != 0 {
		t.Fatal("switch off must not link or create")
	}

	r = newMapRig()
	r.src.s.LinkExisting = true
	u, err := r.mapUser("bob@acme.com", true, nil)
	if err != nil || u.ID != "u-bob" {
		t.Fatalf("got %+v, %v", u, err)
	}
	if len(r.users.linked) != 1 || r.users.linked[0] != "u-bob|https://idp.example.test|sub-9" || len(r.users.created) != 0 {
		t.Fatalf("linked = %v created = %v", r.users.linked, r.users.created)
	}
	if len(r.events) != 1 || r.events[0].Action != "signin.identity_linked" || r.events[0].Target != "user:u-bob" ||
		r.events[0].Detail["provider"] != "oidc" {
		t.Fatalf("events = %+v", r.events)
	}
}

func TestMapUserCreates(t *testing.T) {
	r := newMapRig()
	u, err := r.mapUser("ana@acme.com", true, nil)
	if err != nil || u.ID != "u-new" {
		t.Fatalf("got %+v, %v", u, err)
	}
	if len(r.users.created) != 1 || r.users.created[0] != (authstate.NewUser{Email: "ana@acme.com", Name: "Ana Perez", Role: "editor"}) {
		t.Fatalf("created = %+v", r.users.created)
	}
	if len(r.users.linked) != 1 {
		t.Fatalf("linked = %v", r.users.linked)
	}
	if len(r.events) != 1 || r.events[0].Action != "signin.user_created" || r.events[0].Detail["role"] != "editor" {
		t.Fatalf("events = %+v", r.events)
	}
}

func TestMapUserNameFallback(t *testing.T) {
	r := newMapRig()
	_, err := r.p.MapUser(context.Background(), authstate.MapRequest{
		Identity: authstate.Identity{Issuer: "i", Subject: "s", Email: "ana.perez@acme.com", EmailVerified: true},
		Users:    r.users,
	})
	if err != nil || r.users.created[0].Name != "ana.perez" {
		t.Fatalf("created = %+v, %v", r.users.created, err)
	}
}

func TestMapUserErrors(t *testing.T) {
	r := newMapRig()
	r.users.createErr = authstate.ErrEmailTaken
	_, err := r.mapUser("ana@acme.com", true, nil)
	wantReason(t, err, reasonAccountExists)

	r = newMapRig()
	r.users.createErr = errors.New("db down")
	_, err = r.mapUser("ana@acme.com", true, nil)
	wantReason(t, err, reasonCreateFailed)

	r = newMapRig()
	r.users.linkErr = authstate.ErrIdentityTaken
	_, err = r.mapUser("ana@acme.com", true, nil)
	wantReason(t, err, reasonIdentityTaken)
	if len(r.events) != 0 {
		t.Fatal("audited a failed link")
	}

	r = newMapRig()
	r.src.s.LinkExisting = true
	r.users.linkErr = errors.New("db down")
	_, err = r.mapUser("bob@acme.com", true, nil)
	wantReason(t, err, reasonLinkFailed)
}

// cancelUsers cancels the request context right after Create, like a client
// that disconnects between the two steps; LinkIdentity then honours its ctx.
type cancelUsers struct {
	memUsers
	cancel context.CancelFunc
	ctx    context.Context
}

func (c *cancelUsers) Create(ctx context.Context, n authstate.NewUser) (authstate.User, error) {
	u, err := c.memUsers.Create(ctx, n)
	c.cancel()
	return u, err
}

func (c *cancelUsers) LinkIdentity(ctx context.Context, id, issuer, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.memUsers.LinkIdentity(ctx, id, issuer, subject)
}

func TestMapUserCreateAndLinkSurviveCancel(t *testing.T) {
	r := newMapRig()
	ctx, cancel := context.WithCancel(context.Background())
	users := &cancelUsers{memUsers: *r.users, cancel: cancel}
	_, err := r.p.MapUser(ctx, authstate.MapRequest{
		Identity: authstate.Identity{Issuer: "https://idp.example.test", Subject: "sub-9", Email: "ana@acme.com", EmailVerified: true},
		Users:    users,
	})
	if err != nil || len(users.memUsers.linked) != 1 {
		t.Fatalf("err = %v, linked = %v", err, users.memUsers.linked)
	}
}
