package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/siteconfig"
)

// fakeCSP is a CSPService that records what the handlers asked of it.
type fakeCSP struct {
	state    CSPState
	err      error
	enforced atomic.Int32
}

func (f *fakeCSP) CSPState() CSPState { return f.state }

func (f *fakeCSP) EnforceCSP(context.Context) error {
	f.enforced.Add(1)
	return f.err
}

func newCSPRig(t *testing.T, state CSPState, extra string) (*rig, *fakeCSP, *fakeSite) {
	t.Helper()
	site := newFakeSite("Acme Site")
	site.cfg.ThemeExtra = []byte(extra)
	svc := &fakeCSP{state: state}
	return newRig(t, func(d *Deps) { d.Site = site; d.CSP = svc }), svc, site
}

func TestCSPPromptHidden(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	for _, tc := range []struct {
		name  string
		extra string
		nilC  bool
		want  bool
	}{
		{"nil config", "", true, false},
		{"empty", "", false, false},
		{"other keys only", `{"additional_css":"a{}"}`, false, false},
		{"dismissed now", `{"csp_prompt_hidden":"` + now.Format(time.RFC3339) + `"}`, false, true},
		{"dismissed among others", `{"additional_css":"a{}","csp_prompt_hidden":"` + now.Format(time.RFC3339) + `"}`, false, true},
		{"dismissed 31 days ago", `{"csp_prompt_hidden":"` + now.AddDate(0, 0, -31).Format(time.RFC3339) + `"}`, false, false},
		{"legacy true", `{"csp_prompt_hidden":true}`, false, false},
		{"garbage string", `{"csp_prompt_hidden":"yes"}`, false, false},
		{"invalid JSON", `{"csp_prompt_hidden":`, false, false},
	} {
		var cfg *siteconfig.Config
		if !tc.nilC {
			cfg = &siteconfig.Config{ThemeExtra: []byte(tc.extra)}
		}
		if got := cspPromptHidden(cfg, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCSPCardShownToOwner(t *testing.T) {
	t.Parallel()
	g, _, _ := newCSPRig(t, CSPState{}, "")
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`id="dash-csp"`, "Turn on stricter page protection", "Turn on protection", "How to check first", "Not now",
		`action="/admin/security/page-protection/enforce"`, `action="/admin/security/page-protection/hide"`, cspDocsURL,
		"Technical name: Content Security Policy (CSP)."} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	card := body[strings.Index(body, `id="dash-csp"`):]
	if n := strings.Count(strings.Replace(card, "Technical name: Content Security Policy (CSP).", "", 1), "CSP"); n != 0 {
		t.Errorf("CSP appears %d times outside the help line", n)
	}
	if editor := g.do("GET", "/admin", "editor-token", nil, nil).Body.String(); strings.Contains(editor, "dash-csp") {
		t.Error("an editor sees the card")
	}
}

func TestCSPCardHidden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		state CSPState
		extra string
		noSvc bool
	}{
		{"enforcing", CSPState{Enforcing: true}, "", false},
		{"env forced", CSPState{EnvForced: true}, "", false},
		{"dismissed", CSPState{}, `{"csp_prompt_hidden":"` + time.Now().UTC().Format(time.RFC3339) + `"}`, false},
		{"no service", CSPState{}, "", true},
	} {
		g, _, _ := newCSPRig(t, tc.state, tc.extra)
		if tc.noSvc {
			g.a.deps.CSP = nil
		}
		if body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String(); strings.Contains(body, "dash-csp") {
			t.Errorf("%s: the card is still shown", tc.name)
		}
	}
}

func TestCSPEnforceRoute(t *testing.T) {
	t.Parallel()
	const path = "/admin/security/page-protection/enforce"
	g, svc, _ := newCSPRig(t, CSPState{}, "")
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/" {
		t.Fatalf("owner = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if n := svc.enforced.Load(); n != 1 {
		t.Errorf("EnforceCSP called %d times, want 1", n)
	}
	if rec := g.do("POST", path, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	g.realTokens()
	if rec := g.do("POST", path, "owner-token", nil, map[string]string{"X-CSRF-Token": ""}); rec.Code == http.StatusSeeOther {
		t.Errorf("no token = %d, want a refusal", rec.Code)
	}
	if n := svc.enforced.Load(); n != 1 {
		t.Errorf("refused requests called EnforceCSP: %d calls", n)
	}
}

func TestCSPEnforceRouteEnvForcedAndFailure(t *testing.T) {
	t.Parallel()
	const path = "/admin/security/page-protection/enforce"
	g, svc, _ := newCSPRig(t, CSPState{EnvForced: true}, "")
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("env forced = %d, want 303", rec.Code)
	}
	if n := svc.enforced.Load(); n != 0 {
		t.Errorf("env forced still called EnforceCSP %d times", n)
	}

	g, svc, _ = newCSPRig(t, CSPState{}, "")
	svc.err = errors.New("db down")
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("failure = %d, want 500", rec.Code)
	}

	g.a.deps.CSP = nil
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}

func TestCSPHideRoute(t *testing.T) {
	t.Parallel()
	const path = "/admin/security/page-protection/hide"
	g, _, site := newCSPRig(t, CSPState{}, `{"additional_css":"a{}"}`)
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/" {
		t.Fatalf("hide = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if !cspPromptHidden(&site.cfg, time.Now()) {
		t.Errorf("flag not stored: %s", site.cfg.ThemeExtra)
	}
	if got := string(site.cfg.ThemeExtra); !strings.Contains(got, `"additional_css":"a{}"`) {
		t.Errorf("other keys lost: %s", got)
	}
	if rec := g.do("POST", path, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	g.a.deps.Site = nil
	if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no site = %d, want 503", rec.Code)
	}
}
