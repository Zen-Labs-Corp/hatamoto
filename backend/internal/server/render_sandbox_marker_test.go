package server

import (
	"hatamoto.dev/core/internal/render"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	communityBody  = `<html><body><script src="/p/fancy/assets/plugin.js" defer></script></body></html>`
	firstPartyBody = `<html><body><script src="/p/gtm/assets/plugin.js" defer></script></body></html>`
)

func sandboxedBy(h *renderHandlers, body string) bool {
	rec := httptest.NewRecorder()
	h.writeHTML(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, []byte(body), false)
	return strings.Contains(strings.Join(rec.Header().Values("Content-Security-Policy"), ";"), "sandbox")
}

// The sandbox follows the page: a body that links a community plugin's script
// is sandboxed whatever the live check says, a page without one is decided by
// the live check alone (a required guard), and other origins never
// sandbox by themselves.
func TestWriteHTMLSandboxFollowsBodyScripts(t *testing.T) {
	community := func(name string) bool { return name == "fancy" }
	for _, c := range []struct {
		name string
		body string
		live bool
		want bool
	}{
		{"community script, live off", communityBody, false, true},
		{"community script, live on", communityBody, true, true},
		{"no script, live off", "<p>x</p>", false, false},
		{"no script, live on (required guard)", "<p>x</p>", true, true},
		{"non-community plugin", firstPartyBody, false, false},
		{"path-like name is not a plugin", `<script src="/p/a/b/assets/plugin.js">`, false, false},
		{"unterminated tag", `<script src="/p/fancy/assets/plugin.js`, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := &renderHandlers{communityPlugin: community, communityScripts: func() bool { return c.live }}
			if got := sandboxedBy(h, c.body); got != c.want {
				t.Errorf("sandboxed = %v, want %v", got, c.want)
			}
		})
	}
	if sandboxedBy(&renderHandlers{}, communityBody) {
		t.Error("no lookup wired: nothing is community")
	}
}

// A cached page carries its own marker: a cache hit is sandboxed from the
// cached bytes even though the live check is off.
func TestCacheHitSandboxFromCachedBody(t *testing.T) {
	f := newRenderFixture(t)
	f.h.communityPlugin = func(name string) bool { return name == "fancy" }
	f.h.communityScripts = func() bool { return false }
	f.next = okPage("About")

	fresh := f.get("/p")
	if f.resolves != 1 || len(f.cache.data) != 1 {
		t.Fatalf("setup: resolves=%d cached=%d", f.resolves, len(f.cache.data))
	}
	if strings.Contains(strings.Join(fresh.Header().Values("Content-Security-Policy"), ";"), "sandbox") {
		t.Fatal("a page without the script was sandboxed with live off")
	}
	for k := range f.cache.data {
		f.cache.data[k] = []byte(communityBody)
	}
	hit := f.get("/p")
	if f.resolves != 1 {
		t.Fatalf("second request was not a cache hit (resolves=%d)", f.resolves)
	}
	if !strings.Contains(strings.Join(hit.Header().Values("Content-Security-Policy"), ";"), "sandbox") {
		t.Error("cache hit with a community script was not sandboxed")
	}
}

// The marker matcher must recognise what the real emitter writes: a page
// rendered with a community plugin's script linked is sandboxed with the live
// check off. Fails if the emitted tag shape drifts from the matcher.
func TestRealEmitterTagIsSandboxed(t *testing.T) {
	render.SetPluginScriptGate(func(p string) bool { return p == "fancy" })
	render.SetPluginAssets("fancy", "", "/p/fancy/assets/plugin.js")
	t.Cleanup(func() { render.RemovePlugin("fancy"); render.SetPluginScriptGate(nil) })
	f := newRenderFixture(t)
	f.h.communityPlugin = func(name string) bool { return name == "fancy" }
	f.h.communityScripts = func() bool { return false }
	f.next = okPage("About")
	rec := f.get("/p")
	if !strings.Contains(rec.Body.String(), "/p/fancy/assets/plugin.js") {
		t.Fatalf("emitter did not link the script:\n%s", rec.Body.String())
	}
	if !strings.Contains(strings.Join(rec.Header().Values("Content-Security-Policy"), ";"), "sandbox") {
		t.Error("page linking a community script was not sandboxed by the marker")
	}
}
