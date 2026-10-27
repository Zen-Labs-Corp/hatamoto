package admin

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
)

func TestSecretStoreSingleUseAndUserBound(t *testing.T) {
	t.Parallel()

	var s secretStore
	id := s.putEntry(secretEntry{user: "u1", name: "Invite", key: "https://x/invite/abc", kind: "invite", note: "Emailed to a@b.c"})
	if _, ok := s.take("u2", id); ok {
		t.Fatal("another user took the secret")
	}
	e, ok := s.take("u1", id)
	if !ok || e.kind != "invite" || e.note != "Emailed to a@b.c" || e.key != "https://x/invite/abc" {
		t.Fatalf("take = %+v, %v", e, ok)
	}
	if _, ok := s.take("u1", id); ok {
		t.Fatal("the secret was taken twice")
	}
}

func TestSecretStorePutKeepsAPIKeyKindAndTTL(t *testing.T) {
	t.Parallel()

	var s secretStore
	id := s.put("u1", "Deploy", "hm_live_x")
	s.mu.Lock()
	e := s.m[id]
	s.mu.Unlock()
	if e.kind != "apikey" || e.name != "Deploy" || e.key != "hm_live_x" {
		t.Fatalf("entry = %+v", e)
	}
	if d := time.Until(e.expires); d > secretTTL || d < secretTTL-5*time.Second {
		t.Fatalf("expiry in %v, want about %v", d, secretTTL)
	}
	s.mu.Lock()
	e.expires = time.Now().Add(-time.Second)
	s.m[id] = e
	s.mu.Unlock()
	if _, ok := s.take("u1", id); ok {
		t.Fatal("an expired secret was returned")
	}
}

func TestSecretBannerPartial(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	sec := secretEntry{key: "hm_live_8f3k", note: "Copy it now."}.banner("Copy this key now", "/admin/settings/api-keys")
	out, err := a.RenderPartial("hm/secretbanner", sec)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`role="status"`, `<h2 class="hm-secretbanner__title" id="secret-banner-h">Copy this key now</h2>`, "It will not be shown again.", `>hm_live_8f3k</textarea>`,
		`data-hm-copy-field="secret-value"`, `href="/admin/settings/api-keys"`, `data-hm-dismiss="secret-banner"`, "Copy it now.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("banner lacks %q:\n%s", want, s)
		}
	}
	if strings.Count(strings.ToLower(s), "shown") != 1 {
		t.Errorf("the banner says the secret is shown once, exactly once:\n%s", s)
	}
	if strings.Contains(s, "data-hm-flash") {
		t.Error("a secret banner must never be a flash element")
	}
	if issues := a11y.Check(s, a11y.Options{}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if _, err := a.RenderPartial("hm/secretbanner", ui.Secret{Value: "x", DismissHref: "/"}); err == nil {
		t.Error("a banner without a Heading must fail the required check")
	}
}
