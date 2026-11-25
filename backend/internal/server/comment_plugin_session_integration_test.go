//go:build integration

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

type sessionRig struct {
	t   *testing.T
	s   *Server
	ctx context.Context
}

func newSessionRig(t *testing.T) *sessionRig {
	t.Helper()
	rig := newModerationRig(t)
	idsvc, err := identity.New(identity.Config{
		DB: rig.s.db, Comments: rig.s.comments, Mailer: nopMailer{},
		Site:   func(context.Context) (string, string) { return "Site", "https://site.test" },
		Secret: rig.s.cfg.Secret, Now: time.Now, Logger: rig.s.logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.s.commentIdentity = idsvc
	return &sessionRig{t: t, s: rig.s, ctx: context.Background()}
}

// post runs the callback for a POST with the given Sec-Fetch-Site and cookie.
func (r *sessionRig) post(site string, cookie *http.Cookie, cs sdk.CommenterSession) (*httptest.ResponseRecorder, error) {
	req := httptest.NewRequest(http.MethodPost, "https://site.test/login", nil)
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	return w, r.s.pluginCommenterSession(w, req, "alpha", cs)
}

func hmCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == identity.CookieName {
			return c
		}
	}
	t.Fatal("no hm_c Set-Cookie")
	return nil
}

func (r *sessionRig) device(c *http.Cookie) (uuid.UUID, bool) {
	return r.s.commentIdentity.Device(r.ctx, c.Value)
}

func (r *sessionRig) tokenVersion(id uuid.UUID) int {
	r.t.Helper()
	var v int
	if err := r.s.db.Pool().QueryRow(r.ctx, `SELECT token_version FROM hatamoto_commenters WHERE id=$1`, id).Scan(&v); err != nil {
		r.t.Fatal(err)
	}
	return v
}

func TestPluginCommenterSession(t *testing.T) {
	r := newSessionRig(t)

	// Same-origin sign-in sets a cookie that verifies as the ensured commenter.
	w, err := r.post("same-origin", nil, sdk.CommenterSession{Account: "acct-1"})
	if err != nil {
		t.Fatal(err)
	}
	c := hmCookie(t, w)
	id, ok, err := r.s.comments.PluginCommenter(r.ctx, "alpha", "acct-1")
	if err != nil || !ok {
		t.Fatalf("PluginCommenter: %v %v", ok, err)
	}
	if got, ok := r.device(c); !ok || got != id {
		t.Fatalf("cookie device = %v %v, want %v", got, ok, id)
	}

	// Cross-site or missing Sec-Fetch-Site: refused, no cookie.
	for _, site := range []string{"", "cross-site", "same-site"} {
		w, err := r.post(site, nil, sdk.CommenterSession{Account: "acct-1"})
		if err != pluginhost.ErrSessionCrossOrigin || len(w.Result().Cookies()) != 0 {
			t.Fatalf("site %q: err %v cookies %v", site, err, w.Result().Cookies())
		}
	}

	// Sign out clears the cookie and leaves token_version alone.
	before := r.tokenVersion(id)
	w, err = r.post("same-origin", c, sdk.CommenterSession{SignOut: true})
	if err != nil {
		t.Fatal(err)
	}
	if out := hmCookie(t, w); out.MaxAge >= 0 {
		t.Fatalf("sign-out Max-Age = %d, want < 0", out.MaxAge)
	}
	if r.tokenVersion(id) != before {
		t.Fatal("sign-out changed token_version")
	}

	// max_age caps the cookie lifetime.
	w, err = r.post("same-origin", nil, sdk.CommenterSession{Account: "acct-1", MaxAge: 60})
	if err != nil {
		t.Fatal(err)
	}
	if got := hmCookie(t, w).MaxAge; got != 60 {
		t.Fatalf("Max-Age = %d, want 60", got)
	}

	// Revoking the commenter invalidates the first browser's cookie.
	ident := pluginCommentIdentity{s: r.s, derive: func(p string, id uuid.UUID) string { return comment.PluginCommenterKey("k", p, id) }}
	if revoked, err := ident.Revoke(r.ctx, "alpha", "acct-1"); err != nil || !revoked {
		t.Fatalf("Revoke = %v %v", revoked, err)
	}
	if _, ok := r.device(c); ok {
		t.Fatal("cookie still valid after revoke")
	}
}

func TestPluginCommenterSessionLink(t *testing.T) {
	r := newSessionRig(t)
	mail := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := r.s.db.Pool().QueryRow(r.ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed) VALUES ($1, ''::bytea) RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cookieFor := func(id uuid.UUID) *http.Cookie {
		c, err := r.s.commentIdentity.CookieFor(r.ctx, id, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	origin := func(id uuid.UUID) (p, a string) {
		if err := r.s.db.Pool().QueryRow(r.ctx, `SELECT coalesce(origin_plugin,''), coalesce(origin_account,'') FROM hatamoto_commenters WHERE id=$1`, id).Scan(&p, &a); err != nil {
			t.Fatal(err)
		}
		return
	}

	// An email commenter links to the account and keeps its identity.
	email := mail("h1")
	w, err := r.post("same-origin", cookieFor(email), sdk.CommenterSession{Account: "acct-2", Link: true})
	if err != nil {
		t.Fatal(err)
	}
	if p, a := origin(email); p != "alpha" || a != "acct-2" {
		t.Fatalf("origin = %q %q", p, a)
	}
	if got, ok := r.device(hmCookie(t, w)); !ok || got != email {
		t.Fatalf("cookie device = %v %v, want %v", got, ok, email)
	}

	// An already-bound commenter is not rebound: the cookie is for the account's commenter.
	w, err = r.post("same-origin", cookieFor(email), sdk.CommenterSession{Account: "acct-3", Link: true})
	if err != nil {
		t.Fatal(err)
	}
	if p, a := origin(email); p != "alpha" || a != "acct-2" {
		t.Fatalf("rebound to %q %q", p, a)
	}
	other, ok, err := r.s.comments.PluginCommenter(r.ctx, "alpha", "acct-3")
	if err != nil || !ok || other == email {
		t.Fatalf("acct-3 commenter = %v %v %v", other, ok, err)
	}
	if got, ok := r.device(hmCookie(t, w)); !ok || got != other {
		t.Fatalf("cookie device = %v %v, want %v", got, ok, other)
	}
}
