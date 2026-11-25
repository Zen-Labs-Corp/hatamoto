//go:build integration

package comment

import (
	"testing"

	"github.com/google/uuid"
)

func TestEnsurePluginCommenterIdempotent(t *testing.T) {
	f := newFixture(t)
	a, created, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil || !created || a == uuid.Nil {
		t.Fatalf("first = %v, %v, %v", a, created, err)
	}
	b, created, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil || created || b != a {
		t.Fatalf("second = %v, %v, %v (want %v, false)", b, created, err, a)
	}
	c, created, err := f.svc.EnsurePluginCommenter(f.ctx, "other", "acct-1")
	if err != nil || !created || c == a {
		t.Fatalf("other plugin = %v, %v, %v", c, created, err)
	}
	got, ok, err := f.svc.PluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil || !ok || got != a {
		t.Errorf("PluginCommenter = %v, %v, %v", got, ok, err)
	}
	if _, ok, err := f.svc.PluginCommenter(f.ctx, "plug", "missing"); err != nil || ok {
		t.Errorf("PluginCommenter missing = %v, %v", ok, err)
	}
}

func TestFindCommenterNeverMatchesPluginRow(t *testing.T) {
	f := newFixture(t)
	id, _, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := f.svc.db.Pool().QueryRow(f.ctx, `SELECT email_hash FROM hatamoto_commenters WHERE id = $1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"plugin:x@example.com", hash} {
		if got, ok, err := f.svc.FindCommenter(f.ctx, email); err != nil || ok {
			t.Errorf("FindCommenter(%q) = %v, %v, %v", email, got, ok, err)
		}
	}
}

func TestBindPluginCommenterOnlyUnbound(t *testing.T) {
	f := newFixture(t)
	id, err := f.svc.EnsureCommenter(f.ctx, "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.svc.BindPluginCommenter(f.ctx, id, "plug", "acct-1"); err != nil || !ok {
		t.Fatalf("first bind = %v, %v", ok, err)
	}
	if ok, err := f.svc.BindPluginCommenter(f.ctx, id, "plug", "acct-2"); err != nil || ok {
		t.Errorf("second bind = %v, %v, want false", ok, err)
	}
	other, err := f.svc.EnsureCommenter(f.ctx, "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.svc.BindPluginCommenter(f.ctx, other, "plug", "acct-1"); err != nil || ok {
		t.Errorf("bind to taken account = %v, %v, want false", ok, err)
	}
	if got, ok, _ := f.svc.PluginCommenter(f.ctx, "plug", "acct-1"); !ok || got != id {
		t.Errorf("account owner = %v, %v, want %v", got, ok, id)
	}
}

func TestCommenterEmailOrigin(t *testing.T) {
	f := newFixture(t)
	id, _, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	cm, email, err := f.svc.CommenterEmail(f.ctx, id)
	if err != nil || email != "" || cm.OriginPlugin != "plug" {
		t.Errorf("CommenterEmail = %+v, %q, %v", cm, email, err)
	}
}

func TestStoreAttributesCommenterID(t *testing.T) {
	f := newFixture(t)
	f.enable()
	id, _, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	d := f.draft("", "hello")
	d.CommenterID = &id
	d.NotifyReplies = true
	c := f.store(d)
	if c.CommenterID == nil || *c.CommenterID != id {
		t.Errorf("CommenterID = %v, want %v", c.CommenterID, id)
	}
	if c.NotifyReplies {
		t.Error("NotifyReplies set for an email-less commenter")
	}
}
