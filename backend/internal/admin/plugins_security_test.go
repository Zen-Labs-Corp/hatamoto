package admin

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

const unsignedPassword = "correct-horse-battery"

func unsignedRig(t *testing.T) (*rig, *fakePackages, *sudoAuth) {
	t.Helper()
	fp := &fakePackages{}
	au := &sudoAuth{}
	g := newRig(t, func(d *Deps) { d.Growth.Packages = fp; d.Auth = au })
	g.owner.PasswordHash = "hash"
	return g, fp, au
}

func allGates() url.Values {
	return url.Values{"understand": {"1"}, "host": {"acme.example"}, "password": {unsignedPassword}, "duration": {"24h"}}
}

func postAllow(g *rig, form url.Values) int {
	g.t.Helper()
	return g.do("POST", "/admin/plugins/unsigned", "owner-token", form, nil).Code
}

// Each gate missing or wrong, alone, refuses the request and stores nothing.
func TestAllowUnsignedEachGateAlone(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(url.Values){
		"no acknowledgement": func(v url.Values) { v.Del("understand") },
		"acknowledgement 0":  func(v url.Values) { v.Set("understand", "0") },
		"no host":            func(v url.Values) { v.Del("host") },
		"wrong host":         func(v url.Values) { v.Set("host", "other.example") },
		"no password":        func(v url.Values) { v.Del("password") },
		"wrong password":     func(v url.Values) { v.Set("password", "nope") },
		"no duration":        func(v url.Values) { v.Del("duration") },
		"unknown duration":   func(v url.Values) { v.Set("duration", "7d") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g, fp, _ := unsignedRig(t)
			form := allGates()
			mutate(form)
			if code := postAllow(g, form); code != 422 || fp.unsigned {
				t.Fatalf("status %d, unsigned %v", code, fp.unsigned)
			}
		})
	}
}

// A wrong address never reaches the password check, so it cannot burn an attempt.
func TestAllowUnsignedCheapGatesComeBeforeThePassword(t *testing.T) {
	t.Parallel()

	g, _, au := unsignedRig(t)
	form := allGates()
	form.Set("host", "other.example")
	postAllow(g, form)
	form = allGates()
	form.Del("understand")
	postAllow(g, form)
	if au.calls != 0 {
		t.Errorf("password checked %d times", au.calls)
	}
}

func TestAllowUnsignedAccepted(t *testing.T) {
	t.Parallel()

	now := time.Date(2030, 5, 6, 7, 8, 9, 0, time.UTC)
	old := pluginsNow
	pluginsNow = func() time.Time { return now }
	t.Cleanup(func() { pluginsNow = old })

	for _, tc := range []struct {
		duration string
		until    *time.Time
	}{
		{"1h", ptr(now.Add(time.Hour))},
		{"24h", ptr(now.Add(24 * time.Hour))},
		{"forever", nil},
	} {
		g, fp, _ := unsignedRig(t)
		form := allGates()
		form.Set("duration", tc.duration)
		form.Set("host", "  ACME.example ")
		if code := postAllow(g, form); code != 303 || !fp.unsigned {
			t.Fatalf("%s: status %d, unsigned %v", tc.duration, code, fp.unsigned)
		}
		if (tc.until == nil) != (fp.unsignedEnd == nil) || tc.until != nil && !fp.unsignedEnd.Equal(*tc.until) {
			t.Errorf("%s: until %v, want %v", tc.duration, fp.unsignedEnd, tc.until)
		}
	}
}

func TestAllowUnsignedPasswordless(t *testing.T) {
	t.Parallel()

	g, fp, au := unsignedRig(t)
	g.owner.PasswordHash = ""
	rec := g.do("POST", "/admin/plugins/unsigned", "owner-token", allGates(), nil)
	if rec.Code != 422 || fp.unsigned || au.calls != 0 || !strings.Contains(rec.Body.String(), "Set a password first") {
		t.Fatalf("status %d, unsigned %v, checks %d", rec.Code, fp.unsigned, au.calls)
	}
}

func TestAllowUnsignedWrongPasswordMessageIsGeneric(t *testing.T) {
	t.Parallel()

	g, _, _ := unsignedRig(t)
	form := allGates()
	form.Set("password", "nope")
	rec := g.do("POST", "/admin/plugins/unsigned", "owner-token", form, nil)
	body := rec.Body.String()
	if !strings.Contains(body, sudoMessage) || strings.Contains(body, "nope") {
		t.Errorf("body: %s", body)
	}
}

func TestStopUnsignedIsOneClick(t *testing.T) {
	t.Parallel()

	g, fp, au := unsignedRig(t)
	fp.unsigned, fp.unsignedEnd = true, ptr(time.Now().Add(time.Hour))
	if rec := g.do("POST", "/admin/plugins/unsigned/off", "owner-token", url.Values{}, nil); rec.Code != 303 || fp.unsigned || fp.unsignedEnd != nil || au.calls != 0 {
		t.Fatalf("status %d, unsigned %v, until %v, checks %d", rec.Code, fp.unsigned, fp.unsignedEnd, au.calls)
	}
}

// The Installed page's banner returns to the list; the security page and any
// unknown value return to the security page.
func TestStopUnsignedReturnsWhereItWasPosted(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"installed banner": {url.Values{"return": {"plugins"}}, "/admin/plugins"},
		"security page":    {url.Values{}, "/admin/plugins/security"},
		"unknown value":    {url.Values{"return": {"https://elsewhere.example/"}}, "/admin/plugins/security"},
		"another known":    {url.Values{"return": {"browse"}}, "/admin/plugins/security"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g, fp, _ := unsignedRig(t)
			fp.unsigned = true
			rec := g.do("POST", "/admin/plugins/unsigned/off", "owner-token", tc.form, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != tc.want || fp.unsigned {
				t.Fatalf("status %d to %q, unsigned %v; want 303 to %q", rec.Code, rec.Header().Get("Location"), fp.unsigned, tc.want)
			}
		})
	}
}

func TestUnsignedRoutesAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	t.Parallel()

	g, fp, _ := unsignedRig(t)
	for _, p := range []string{"/admin/plugins/unsigned", "/admin/plugins/unsigned/off"} {
		if rec := g.do("POST", p, "editor-token", allGates(), nil); rec.Code != 403 {
			t.Errorf("%s as editor: %d", p, rec.Code)
		}
	}
	g.realTokens()
	for _, p := range []string{"/admin/plugins/unsigned", "/admin/plugins/unsigned/off"} {
		if rec := g.do("POST", p, "owner-token", allGates(), map[string]string{"X-CSRF-Token": ""}); rec.Code < 400 || fp.unsigned {
			t.Errorf("%s without a token: %d, unsigned %v", p, rec.Code, fp.unsigned)
		}
	}
}
