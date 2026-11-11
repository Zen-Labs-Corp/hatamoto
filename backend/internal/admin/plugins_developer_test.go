package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() { areaPages["plugins-security-developer"] = []string{"plugins/securitydeveloper"} }

func developerGates() url.Values {
	return url.Values{"understand": {"1"}, "host": {"acme.example"}, "password": {unsignedPassword}}
}

func postDeveloper(g *rig, form url.Values) int {
	g.t.Helper()
	return g.do("POST", "/admin/plugins/developer", "owner-token", form, nil).Code
}

// Each gate missing or wrong, alone, refuses the request and stores nothing.
func TestAllowDeveloperEachGateAlone(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(url.Values){
		"no acknowledgement": func(v url.Values) { v.Del("understand") },
		"acknowledgement 0":  func(v url.Values) { v.Set("understand", "0") },
		"no host":            func(v url.Values) { v.Del("host") },
		"wrong host":         func(v url.Values) { v.Set("host", "other.example") },
		"no password":        func(v url.Values) { v.Del("password") },
		"wrong password":     func(v url.Values) { v.Set("password", "nope") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g, fp, _ := unsignedRig(t)
			form := developerGates()
			mutate(form)
			if code := postDeveloper(g, form); code != 422 || fp.developerOn {
				t.Fatalf("status %d, developer %v", code, fp.developerOn)
			}
		})
	}
}

// A wrong address or a missing tick never reaches the password check.
func TestAllowDeveloperCheapGatesComeBeforeThePassword(t *testing.T) {
	t.Parallel()

	g, _, au := unsignedRig(t)
	form := developerGates()
	form.Set("host", "other.example")
	postDeveloper(g, form)
	form = developerGates()
	form.Del("understand")
	postDeveloper(g, form)
	if au.calls != 0 {
		t.Errorf("password checked %d times", au.calls)
	}
}

func TestAllowDeveloperNeedsAPassword(t *testing.T) {
	t.Parallel()

	g, fp, au := unsignedRig(t)
	g.owner.PasswordHash = ""
	rec := g.do("POST", "/admin/plugins/developer", "owner-token", developerGates(), nil)
	if rec.Code != 422 || fp.developerOn || au.calls != 0 || !strings.Contains(rec.Body.String(), "Set a password first") {
		t.Fatalf("status %d, developer %v, checks %d", rec.Code, fp.developerOn, au.calls)
	}
}

func TestAllowDeveloperAccepted(t *testing.T) {
	t.Parallel()

	g, fp, _ := unsignedRig(t)
	form := developerGates()
	form.Set("host", "  ACME.example ")
	if code := postDeveloper(g, form); code != 303 || !fp.developerOn || fp.unsigned {
		t.Fatalf("status %d, developer %v, unsigned %v", code, fp.developerOn, fp.unsigned)
	}
}

func TestStopDeveloperIsOneClick(t *testing.T) {
	t.Parallel()

	g, fp, au := unsignedRig(t)
	fp.developerOn = true
	if rec := g.do("POST", "/admin/plugins/developer/off", "owner-token", url.Values{}, nil); rec.Code != 303 || fp.developerOn || au.calls != 0 {
		t.Fatalf("status %d, developer %v, checks %d", rec.Code, fp.developerOn, au.calls)
	}
}

// The turn-off form returns to the page it was posted from.
func TestStopDeveloperReturnTargets(t *testing.T) {
	t.Parallel()

	for ret, want := range map[string]string{"": "/admin/plugins/security", "plugins": "/admin/plugins", "theme": "/admin/theme/themes", "elsewhere": "/admin/plugins/security"} {
		g, fp, _ := unsignedRig(t)
		fp.developerOn = true
		rec := g.do("POST", "/admin/plugins/developer/off", "owner-token", url.Values{"return": {ret}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != want || fp.developerOn {
			t.Errorf("return=%q: status %d, location %q, developer %v", ret, rec.Code, rec.Header().Get("Location"), fp.developerOn)
		}
	}
}

// The gate script wants a duration the developer switch does not have; the
// page posts a fixed one and the handler never reads it.
func TestAllowDeveloperIgnoresDuration(t *testing.T) {
	t.Parallel()

	for _, d := range []string{"", "forever", "1h", "bogus"} {
		g, fp, _ := unsignedRig(t)
		form := developerGates()
		form.Set("duration", d)
		if code := postDeveloper(g, form); code != 303 || !fp.developerOn || fp.unsigned {
			t.Errorf("duration %q: status %d, developer %v, unsigned %v", d, code, fp.developerOn, fp.unsigned)
		}
	}
}

func TestDeveloperGatesPage(t *testing.T) {
	t.Parallel()

	g, fp, _ := unsignedRig(t)
	rec := g.do("GET", "/admin/plugins/security/developer", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Allow developer packages",
		"Developer packages run code written by people Hatamoto has not reviewed, with every permission you grant them.",
		`action="/admin/plugins/developer"`, `name="understand"`, "I understand", "Type your site address to confirm", `name="host"`,
		"Type acme.example exactly.", "Your password", `name="password"`, `autocomplete="current-password"`,
		`data-hm-gate`, `data-hm-gate-host="acme.example"`, `data-hm-gate-submit`, `name="duration" value="forever"`, "Cancel"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `value="1h"`) || strings.Contains(body, "How long") {
		t.Error("the developer page asks for a duration")
	}
	if _, form, ok := strings.Cut(body, `action="/admin/plugins/developer"`); !ok || !strings.HasPrefix(form[strings.Index(form, ">")+1:], `<input type="hidden" name="_csrf"`) {
		t.Error("csrf is not the first field of the form")
	}
	if got := strings.Join(trailLabels(t, body), " / "); got != "Home / Plugins / Package security / Allow developer packages" {
		t.Errorf("crumbs %q", got)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	// Already on: nothing to ask.
	fp.developerOn = true
	if rec := g.do("GET", "/admin/plugins/security/developer", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/plugins/security" {
		t.Errorf("when on: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	// Owner only.
	if rec := g.do("GET", "/admin/plugins/security/developer", "editor-token", nil, nil); rec.Code == 200 {
		t.Error("an editor reached the developer page")
	}
}

func TestDeveloperGatesPagePasswordless(t *testing.T) {
	t.Parallel()

	g, _, _ := unsignedRig(t)
	g.owner.PasswordHash = ""
	body := g.do("GET", "/admin/plugins/security/developer", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Set a password first") || strings.Contains(body, `name="password"`) {
		t.Error("a passwordless owner must be sent to the profile, not shown a gate form")
	}
}

// The Package security copy names no site: the developer index is configurable.
func TestPackageSecurityDeveloperCopyIsNeutral(t *testing.T) {
	t.Parallel()

	g, _, _ := unsignedRig(t)
	rec := g.do("GET", "/admin/plugins/security", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Developer packages") || strings.Contains(rec.Body.String(), "developers on hatamoto.dev") {
		t.Errorf("status %d, body names a site or lacks the developer card", rec.Code)
	}
}
