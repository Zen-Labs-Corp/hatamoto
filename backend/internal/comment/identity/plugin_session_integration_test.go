//go:build integration

package identity

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

func (e *idEnv) pluginCommenter(account string) uuid.UUID {
	e.t.Helper()
	id, _, err := e.svc.EnsurePluginCommenter(e.ctx, "plug", account)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestCookieForSignsAndCaps(t *testing.T) {
	e := newIDEnv(t)
	id := e.pluginCommenter("acct-1")
	ck, err := e.id.CookieFor(e.ctx, id, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := e.id.Device(e.ctx, ck.Value); !ok || got != id {
		t.Errorf("Device = %v, %v, want %v", got, ok, id)
	}
	site := int(e.id.cookieLifetime(e.ctx) / time.Second)
	if ck.MaxAge != site || !ck.Secure || !ck.HttpOnly {
		t.Errorf("cookie = %+v, want MaxAge %d", ck, site)
	}
	capped, err := e.id.CookieFor(e.ctx, id, false, time.Hour)
	if err != nil || capped.MaxAge != 3600 {
		t.Errorf("capped = %+v, %v", capped, err)
	}
	longer, err := e.id.CookieFor(e.ctx, id, false, 10*365*24*time.Hour)
	if err != nil || longer.MaxAge != site {
		t.Errorf("longer cap = %+v, %v, want site lifetime %d", longer, err, site)
	}
	if _, err := e.id.CookieFor(e.ctx, uuid.New(), false, 0); !errors.Is(err, comment.ErrNotFound) {
		t.Errorf("unknown commenter = %v, want ErrNotFound", err)
	}
}

func TestCookieForRevokeAndSignOut(t *testing.T) {
	e := newIDEnv(t)
	id := e.pluginCommenter("acct-1")
	ck, err := e.id.CookieFor(e.ctx, id, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := e.id.store.TokenVersion(e.ctx, id)
	if clear := e.id.ClearCookie(false); clear.MaxAge >= 0 {
		t.Errorf("ClearCookie MaxAge = %d, want negative", clear.MaxAge)
	}
	if after, _, _ := e.id.store.TokenVersion(e.ctx, id); after != before {
		t.Errorf("token_version %d -> %d after sign-out", before, after)
	}
	if _, ok := e.id.Device(e.ctx, ck.Value); !ok {
		t.Error("cookie rejected after sign-out")
	}
	if _, err := e.svc.RevokeDevices(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.id.Device(e.ctx, ck.Value); ok {
		t.Error("cookie valid after RevokeDevices")
	}
}

func TestPurgeKeepsPluginCommenter(t *testing.T) {
	e := newIDEnv(t)
	plug := e.pluginCommenter("acct-1")
	mail, err := e.svc.EnsureCommenter(e.ctx, "idle@example.test")
	if err != nil {
		t.Fatal(err)
	}
	e.clock.advance(25 * time.Hour)
	e.exec(`UPDATE hatamoto_commenters SET last_seen_at = last_seen_at - interval '25 hours'`)
	if err := e.id.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	var keptPlugin, keptMail int
	e.scalar(&keptPlugin, `SELECT count(*) FROM hatamoto_commenters WHERE id = $1`, plug)
	e.scalar(&keptMail, `SELECT count(*) FROM hatamoto_commenters WHERE id = $1`, mail)
	if keptPlugin != 1 || keptMail != 0 {
		t.Errorf("after purge plugin rows = %d (want 1), idle email rows = %d (want 0)", keptPlugin, keptMail)
	}
}
