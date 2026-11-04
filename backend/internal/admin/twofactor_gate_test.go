package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestTwoFactorGatePolicies(t *testing.T) {
	t.Parallel()

	const security = "/admin/profile/security"
	cases := []struct {
		policy, token string
		gated         bool
	}{
		{siteconfig.TwoFactorOptional, "owner-token", false},
		{siteconfig.TwoFactorOptional, "editor-token", false},
		{siteconfig.TwoFactorOwners, "owner-token", true},
		{siteconfig.TwoFactorOwners, "editor-token", false},
		{siteconfig.TwoFactorEveryone, "owner-token", true},
		{siteconfig.TwoFactorEveryone, "editor-token", true},
	}
	for _, c := range cases {
		g := newTwoFactorSecRig(t)
		g.setPolicy(c.policy)
		rec := g.do("GET", "/admin/", c.token, nil, nil)
		gated := rec.Code == 303 && rec.Header().Get("Location") == security
		if gated != c.gated {
			t.Errorf("%s/%s: gated=%v (%d %s), want %v", c.policy, c.token, gated, rec.Code, rec.Header().Get("Location"), c.gated)
		}
	}
}

func TestTwoFactorGateLetsAUserWithAFactorThrough(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.setPolicy(siteconfig.TwoFactorEveryone)
	g.tf.totpOn = true
	if rec := g.do("GET", "/admin/", "owner-token", nil, nil); rec.Code != 200 {
		t.Fatalf("with a factor: %d", rec.Code)
	}
}

func TestTwoFactorGateExemptPaths(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.setPolicy(siteconfig.TwoFactorEveryone)
	for _, p := range []string{"/admin/profile/security", "/admin/profile", "/admin/sudo"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code == 303 && rec.Header().Get("Location") == "/admin/profile/security" {
			t.Errorf("GET %s is gated", p)
		}
	}
	rec := g.do("POST", "/admin/logout", "owner-token", nil, nil)
	if rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "/login") {
		t.Errorf("logout: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTwoFactorGateHTMXAndPOST(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.setPolicy(siteconfig.TwoFactorOwners)
	rec := g.do("GET", "/admin/", "owner-token", nil, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") != "/admin/profile/security" {
		t.Errorf("htmx: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	rec = g.do("POST", "/admin/theme", "owner-token", nil, nil)
	if rec.Code == 303 && rec.Header().Get("Location") == "/admin/profile/security" {
		t.Error("the theme switch is gated")
	}
	rec = g.do("POST", "/admin/settings/users/two-factor", "owner-token", map[string][]string{"policy": {"everyone"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/profile/security" {
		t.Errorf("POST: %d %q (redirect carries no next)", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTwoFactorGateFailsOpenOnLookupErrorAndIsOffInSetup(t *testing.T) {
	t.Parallel()

	g := newTwoFactorSecRig(t)
	g.setPolicy(siteconfig.TwoFactorEveryone)
	g.tf.hasErr = context.DeadlineExceeded
	if rec := g.do("GET", "/admin/", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("lookup error: %d, the gate must let the request through", rec.Code)
	}
	g2 := newTwoFactorSecRig(t)
	g2.a.deps.TwoFactor = nil
	g2.setPolicy(siteconfig.TwoFactorEveryone)
	if rec := g2.do("GET", "/admin/", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("no provider: %d", rec.Code)
	}
}
