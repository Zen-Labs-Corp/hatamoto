package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/twofactor"
)

type twoFactorSecRig struct {
	*twoStepRig
	mu     sync.Mutex
	events []AuditEvent
}

func newTwoFactorSecRig(t *testing.T) *twoFactorSecRig {
	t.Helper()
	g := &twoFactorSecRig{twoStepRig: newTwoStepRig(t)}
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) {
		g.mu.Lock()
		g.events = append(g.events, e)
		g.mu.Unlock()
	}
	g.tf.uid, g.tf.secure, g.tf.confirm = g.owner.ID, true, "424242"
	g.a.deps.Passkeys = &fakePasskeys{owner: g.owner.ID}
	return g
}

func (g *twoFactorSecRig) actions() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, e := range g.events {
		out = append(out, e.Action)
	}
	return out
}

func (g *twoFactorSecRig) grant() { g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID) }

func (g *twoFactorSecRig) setPolicy(p string) { g.a.deps.Site.(*fakeSite).cfg.TwoFactorPolicy = p }

func TestSecurityPageShowsStatusAndGolden(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	g.tf.totpOn, g.tf.left = true, 7
	g.tf.keys = []twofactor.Passkey{{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "Laptop <b>", CreatedAt: now}}
	rec := g.do("GET", "/admin/profile/security", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"7 of 10 left", "Laptop &lt;b&gt;", "Remove", "Add a passkey"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Laptop <b>") {
		t.Error("passkey name is not escaped")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/profile-security.html", normalizePage(body))
}

func TestSecurityEnrolmentFlow(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	// Without a confirmed password the form redirects to the sudo page.
	if rec := g.do("GET", "/admin/profile/security/totp", "owner-token", nil, nil); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/sudo") {
		t.Fatalf("no sudo: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	g.grant()
	// A GET writes nothing: with no enrolment begun it goes back to the Security page.
	if rec := g.do("GET", "/admin/profile/security/totp", "owner-token", nil, nil); rec.Code != 303 || g.tf.pending {
		t.Fatalf("GET without begin: %d pending=%v", rec.Code, g.tf.pending)
	}
	if rec := g.do("POST", "/admin/profile/security/totp/begin", "owner-token", url.Values{}, nil); rec.Code != 303 || !g.tf.pending {
		t.Fatalf("begin: %d pending=%v", rec.Code, g.tf.pending)
	}
	rec := g.do("GET", "/admin/profile/security/totp", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "<svg") || !strings.Contains(body, "JBSW Y3DP EHPK 3PXP") {
		t.Fatalf("enrol page: %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/profile-security-totp.html", normalizePage(body))

	if rec := g.do("POST", "/admin/profile/security/totp", "owner-token", url.Values{"code": {"000000"}}, nil); rec.Code != 422 || g.tf.totpOn {
		t.Fatalf("wrong code: %d on=%v", rec.Code, g.tf.totpOn)
	}
	rec = g.do("POST", "/admin/profile/security/totp", "owner-token", url.Values{"code": {"424242"}}, nil)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.Contains(loc, "/profile/security/codes?id=") || !g.tf.totpOn {
		t.Fatalf("confirm: %d %s", rec.Code, loc)
	}
	codes := g.do("GET", loc, "owner-token", nil, nil)
	cb := codes.Body.String()
	if !strings.Contains(cb, "AAAA-BBBB-CCCC") || codes.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("codes page: %d", codes.Code)
	}
	if issues := a11y.Check(cb, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("codes a11y: %v", issues)
	}
	compareGolden(t, "pages/profile-security-codes.html", normalizePage(cb))
	if again := g.do("GET", loc, "owner-token", nil, nil).Body.String(); strings.Contains(again, "AAAA-BBBB-CCCC") {
		t.Error("recovery codes shown twice")
	}
	if got := g.actions(); len(got) != 1 || got[0] != "user.totp.enable" {
		t.Errorf("audit: %v", got)
	}
	for _, e := range g.events {
		if strings.Contains(strings.Join(mapStrings(e.Detail), " "), "AAAA") {
			t.Error("a secret reached the audit log")
		}
	}
}

func mapStrings(m map[string]any) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+strings.TrimSpace(strings.ReplaceAll(fmtAny(v), "\n", " ")))
	}
	return out
}

func fmtAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestSecurityRemoveAndRegenerateAudit(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	pk := uuid.New()
	g.tf.totpOn, g.tf.left = true, 3
	g.tf.keys = []twofactor.Passkey{{ID: pk, Name: "Key"}}
	g.grant()
	if rec := g.do("POST", "/admin/profile/security/codes", "owner-token", url.Values{}, nil); rec.Code != 303 || g.tf.left != 10 {
		t.Fatalf("regenerate: %d left=%d", rec.Code, g.tf.left)
	}
	if rec := g.do("POST", "/admin/profile/security/passkeys/"+pk.String()+"/remove", "owner-token", url.Values{}, nil); rec.Code != 303 || len(g.tf.keys) != 0 {
		t.Fatalf("remove passkey: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/profile/security/totp/remove", "owner-token", url.Values{}, nil); rec.Code != 303 || g.tf.totpOn {
		t.Fatalf("remove totp: %d", rec.Code)
	}
	want := []string{"user.recovery_codes.regenerate", "user.passkey.remove", "user.totp.disable"}
	if got := strings.Join(g.actions(), ","); got != strings.Join(want, ",") {
		t.Errorf("audit %s, want %v", got, want)
	}
}

func TestSecurityMutationsNeedSudo(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.tf.totpOn = true
	for _, p := range []string{"/admin/profile/security/totp/begin", "/admin/profile/security/totp", "/admin/profile/security/totp/remove", "/admin/profile/security/codes"} {
		if rec := g.do("POST", p, "owner-token", url.Values{"code": {"424242"}}, nil); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/sudo") {
			t.Errorf("%s without sudo: %d %s", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	if !g.tf.totpOn || len(g.actions()) != 0 {
		t.Error("a mutation ran without sudo")
	}
	if rec := g.postJSON("/admin/profile/security/passkeys/begin", "owner-token", "{}", nil); rec.Code != 403 {
		t.Errorf("passkey begin without sudo: %d", rec.Code)
	}
}

func TestSecurityLastFactorGuard(t *testing.T) {
	t.Parallel()

	for policy, want := range map[string]bool{siteconfig.TwoFactorOptional: false, siteconfig.TwoFactorOwners: true, siteconfig.TwoFactorEveryone: true} {
		g := newTwoFactorSecRig(t)
		g.setPolicy(policy)
		g.tf.totpOn = true
		g.grant()
		rec := g.do("POST", "/admin/profile/security/totp/remove", "owner-token", url.Values{}, nil)
		if rec.Code != 303 || g.tf.totpOn != want {
			t.Errorf("policy %s: removal refused=%v, want %v", policy, g.tf.totpOn, want)
		}
	}
	// An editor under "owners" may remove their last factor; under "everyone" not.
	g := newTwoFactorSecRig(t)
	g.setPolicy(siteconfig.TwoFactorOwners)
	if g.requires("editor") || !g.requires("owner") {
		t.Error("owners policy must bind owners only")
	}
}

func (g *twoFactorSecRig) requires(role string) bool {
	return g.a.requiresFactor(context.Background(), role)
}

func TestSecurityPasskeyEnrolmentJSON(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.grant()
	rec := g.postJSON("/admin/profile/security/passkeys/begin", "owner-token", "{}", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ceremony"`) {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
}

func TestSecurityPasskeyAddPage(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	if rec := g.do("GET", "/admin/profile/security/passkeys/add", "owner-token", nil, nil); rec.Code != 303 {
		t.Fatalf("without sudo: %d", rec.Code)
	}
	g.grant()
	rec := g.do("GET", "/admin/profile/security/passkeys/add", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data-passkey-register") {
		t.Fatalf("add page: %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestSecurityFirstPasskeyGetsRecoveryCodes(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.grant()
	var begun struct {
		Ceremony string `json:"ceremony"`
	}
	if err := json.Unmarshal(g.postJSON("/admin/profile/security/passkeys/begin", "owner-token", "{}", nil).Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	rec := g.postJSON("/admin/profile/security/passkeys/finish?c="+begun.Ceremony+"&name=Key", "owner-token", "{}", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/profile/security/codes?id=") || g.tf.left != 10 {
		t.Fatalf("finish: %d %s left=%d", rec.Code, rec.Body, g.tf.left)
	}
	// A user who already has codes is sent back to the Security page.
	g2 := newTwoFactorSecRig(t)
	g2.grant()
	g2.tf.left = 4
	_ = json.Unmarshal(g2.postJSON("/admin/profile/security/passkeys/begin", "owner-token", "{}", nil).Body.Bytes(), &begun)
	rec = g2.postJSON("/admin/profile/security/passkeys/finish?c="+begun.Ceremony, "owner-token", "{}", nil)
	if strings.Contains(rec.Body.String(), "codes?id=") || g2.tf.left != 4 {
		t.Fatalf("existing codes were replaced: %s left=%d", rec.Body, g2.tf.left)
	}
}

func TestSecurityChangesSignOutOtherSessions(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"/admin/profile/security/totp/remove", "/admin/profile/security/codes"} {
		g := newTwoFactorSecRig(t)
		g.tf.totpOn = true
		g.grant()
		if rec := g.do("POST", p, "owner-token", url.Values{}, nil); rec.Code != 303 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
		g.sessions.mu.Lock()
		_, other := g.sessions.byToken["idle-token"]
		_, own := g.sessions.byToken["owner-token"]
		g.sessions.mu.Unlock()
		if other || !own {
			t.Errorf("%s: other session alive=%v, own session alive=%v", p, other, own)
		}
	}
}
