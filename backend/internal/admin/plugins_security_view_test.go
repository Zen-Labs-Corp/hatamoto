package admin

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() { areaPages["pluginsecurity"] = []string{"plugins/security"} }

func pkgSecurityRig(t *testing.T, fp *fakePackages) *rig {
	t.Helper()
	g := newRig(t, func(d *Deps) { d.Growth.Packages = fp })
	g.owner.PasswordHash = "hash"
	return g
}

func getSecurity(g *rig, token string) (int, string) {
	g.t.Helper()
	rec := g.do("GET", "/admin/plugins/security", token, nil, nil)
	return rec.Code, rec.Body.String()
}

func TestSecurityPageOff(t *testing.T) {
	t.Parallel()

	g := pkgSecurityRig(t, &fakePackages{signers: []SignerInfo{
		{Name: "First-party key", Algorithm: "ed25519", Fingerprint: "SHA256:abc", Source: "Built in"},
		{Name: "Custom signer k1", Algorithm: "ed25519", Fingerprint: "SHA256:def", Source: "Environment"},
	}})
	code, body := getSecurity(g, "owner-token")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"Signed packages only", "Protected", "First-party key", "SHA256:def", "Environment",
		"Signers are set by whoever runs the server, not from this screen.",
		`<details class="hm-disclosure hm-pluginsecurity__advanced">`, "Advanced: allow unsigned packages",
		`action="/admin/plugins/unsigned"`, "Type acme.example exactly.", `name="understand"`, `name="host"`, `name="password"`,
		`value="1h"`, `value="24h"`, `value="forever"`, "Until I turn it off", "Allow unsigned packages"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Stop allowing") || strings.Contains(body, "checked") {
		t.Error("off state shows the stop button or preselects a choice")
	}
	if strings.Contains(body, " disabled") {
		t.Error("the submit button is disabled in server HTML")
	}
	if got := strings.Join(trailLabels(t, body), " / "); got != "Home / Plugins / Package security" {
		t.Errorf("crumbs %q", got)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestSecurityPageOn(t *testing.T) {
	t.Parallel()

	until := time.Date(2030, 1, 2, 3, 4, 0, 0, time.UTC)
	g := pkgSecurityRig(t, &fakePackages{unsigned: true, unsignedEnd: &until})
	_, body := getSecurity(g, "owner-token")
	for _, want := range []string{"Unsigned packages allowed", "until 2 Jan 2030 03:04 UTC", "Stop allowing unsigned packages", `action="/admin/plugins/unsigned/off"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Advanced: allow unsigned packages") {
		t.Error("the allow form is shown while unsigned is on")
	}
	forever := pkgSecurityRig(t, &fakePackages{unsigned: true})
	if _, body := getSecurity(forever, "owner-token"); !strings.Contains(body, "until you turn it off") {
		t.Error("no expiry: want the open-ended wording")
	}
}

func TestSecurityPagePasswordless(t *testing.T) {
	t.Parallel()

	g := pkgSecurityRig(t, &fakePackages{})
	g.owner.PasswordHash = ""
	_, body := getSecurity(g, "owner-token")
	if !strings.Contains(body, "Set a password first") || !strings.Contains(body, `href="/admin/profile"`) {
		t.Error("passwordless owner: want the set-a-password message and link")
	}
	if strings.Contains(body, `name="understand"`) {
		t.Error("the allow form is shown to a passwordless owner")
	}
}

func TestSecurityPageOwnerOnly(t *testing.T) {
	t.Parallel()

	g := pkgSecurityRig(t, &fakePackages{})
	if code, _ := getSecurity(g, "editor-token"); code != 403 {
		t.Errorf("editor: %d", code)
	}
}
