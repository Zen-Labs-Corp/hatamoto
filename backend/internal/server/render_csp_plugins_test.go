package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
)

func TestRenderCSPPluginHosts(t *testing.T) {
	cspOf := func(w *httptest.ResponseRecorder) string {
		return w.Header().Get("Content-Security-Policy-Report-Only")
	}
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.engine = struct{ pageRenderer }{f.h.engine} // a theme without csp.strictScripts

	if got := cspOf(f.get("/about")); got != renderCSP {
		t.Fatalf("no plugin: CSP = %q", got)
	}

	set := pluginhost.CSPSet{Script: []string{"https://js.example.com"}, Connect: []string{"https://api.example.com"},
		Img: []string{"https://img.example.com"}, Frame: []string{"https://f.example.com"}}
	f.h.pluginCSP = func() pluginhost.CSPSet { return set }
	// The page is cached already: the hosts reach the cache hit as well.
	csp := cspOf(f.get("/about"))
	if f.resolves != 1 {
		t.Fatalf("expected a cache hit, resolves = %d", f.resolves)
	}
	for _, want := range []string{"script-src 'self' 'unsafe-inline' https://js.example.com;", "connect-src 'self' https://api.example.com;"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP = %q, missing %q", csp, want)
		}
	}
	// The base already allows every https image and frame: nothing is added.
	if strings.Contains(csp, "img.example.com") || strings.Contains(csp, "f.example.com") || !strings.Contains(csp, "img-src 'self' data: https:;") {
		t.Errorf("img and frame stay as they are: %q", csp)
	}

	// Plugin hosts and the captcha provider share the one builder.
	f.h.cspExtras = func(context.Context) cspExtras { return captchaExtras("turnstile") }
	if csp = cspOf(f.get("/about")); !strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com https://js.example.com;") {
		t.Errorf("provider + plugin CSP = %q", csp)
	}

	// Disabled (no hosts left): back to the default.
	f.h.cspExtras = nil
	set = pluginhost.CSPSet{}
	if got := cspOf(f.get("/about")); got != renderCSP {
		t.Errorf("after disabling: CSP = %q", got)
	}
}

func TestRenderCSPPluginHostsStrictTheme(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.pluginCSP = func() pluginhost.CSPSet { return pluginhost.CSPSet{Script: []string{"https://js.example.com"}} }
	got := f.get("/about").Header().Get("Content-Security-Policy-Report-Only")
	if !strings.Contains(got, "script-src 'self' 'inline-speculation-rules' https://js.example.com;") || strings.Contains(strings.SplitN(got, "style-src", 2)[0], "unsafe-inline") {
		t.Errorf("strict theme CSP = %q", got)
	}
}

func TestAddSourcesImgWithoutHTTPS(t *testing.T) {
	base := "default-src 'self'; img-src 'self' data:; frame-src 'self'"
	got := cspWithExtras(base, pluginExtras(pluginhost.CSPSet{Img: []string{"https://i.example.com"}, Frame: []string{"https://f.example.com"}}))
	if got != "default-src 'self'; img-src 'self' data: https://i.example.com; frame-src 'self' https://f.example.com" {
		t.Errorf("got %q", got)
	}
}
