package admin

import (
	"net/url"
	"strings"
	"testing"
)

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
