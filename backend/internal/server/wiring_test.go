package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// setupPackages must install both package policy hooks on the plugin runtime.
// The runtime offers no way to read them back, so this pins the wiring in the
// source: removing either call fails here.
func TestSetupPackagesWiresPolicyHooks(t *testing.T) {
	src, err := os.ReadFile("packages.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		"s.plugins.SetGrantPolicy(s.packages.CheckGrant)",
		"s.plugins.SetScriptsDeny(s.packages.ScriptsRefused)",
	} {
		if !strings.Contains(string(src), call) {
			t.Errorf("setupPackages no longer calls %s", call)
		}
	}
}

// The handlers the real server builds sandbox a page exactly when a community
// script is live (communityScriptsLive feeds writeHTML).
func TestRealRenderHandlersSandboxFollowsCommunityLive(t *testing.T) {
	for _, live := range []bool{false, true} {
		s := &Server{communityLive: func() bool { return live }}
		h := s.newRenderHandlers(nil, nil)
		rec := httptest.NewRecorder()
		h.writeHTML(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, []byte("<p>x</p>"), false)
		got := strings.Contains(strings.Join(rec.Header().Values("Content-Security-Policy"), ";"), "sandbox")
		if got != live {
			t.Errorf("live=%v: sandbox header present=%v", live, got)
		}
	}
}

// Every public CORS mount of the real router answers an Origin: null
// preflight, on the default prefix and the configured one, and answers no
// other origin.
func TestRealRouterPreflightMounts(t *testing.T) {
	h, _, _ := prefixServer(t, "/x/v1")
	paths := []string{"/forms/contact/submit"}
	for _, base := range []string{"/api/v1/public", "/x/v1"} {
		paths = append(paths, base+"/forms/contact/token", base+"/forms/contact/submit")
	}
	paths = append(paths, "/api/v1/track", "/x/v1/track")
	for _, p := range paths {
		rec := prefixServe(h, http.MethodOptions, p, "", "Origin", "null")
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
			t.Errorf("OPTIONS %s null: %d %v", p, rec.Code, rec.Header())
		}
		rec = prefixServe(h, http.MethodOptions, p, "", "Origin", "https://evil.example")
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("OPTIONS %s other origin got ACAO %q", p, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}
