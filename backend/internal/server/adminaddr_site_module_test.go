package server

import (
	"context"
	"net/http"
	"testing"

	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/siteaccess"
)

func TestModulePrefixIsAFixedRoutePrefix(t *testing.T) {
	t.Cleanup(modhost.Reset)
	if computeFixedRoutePrefixes()["example"] {
		t.Fatal("example is reserved with nothing registered")
	}
	modhost.RegisterRoute(modhost.Route{Module: "example", Prefix: "/example/", Handler: http.NotFoundHandler()})
	if !computeFixedRoutePrefixes()["example"] {
		t.Fatal("a module prefix is not refused as an admin address")
	}
	modhost.Reset()
	if computeFixedRoutePrefixes()["example"] {
		t.Fatal("example is still reserved after Reset")
	}
}

// A module prefix is matched on the escaped path the mux routes on, and only
// when that path is clean with no encoded slash. Anything else takes the public
// chain, where the site gate applies; the module itself is not gated (it is
// anonymous at the core level), so reaching it under maintenance is the signal.
func TestModulePrefixMatchUsesEscapedCleanPath(t *testing.T) {
	_, s := moduleServer(t,
		modhost.Route{Module: "scim", Prefix: "/scim/", Handler: okHandler(nil)},
		modhost.Route{Module: "example", Prefix: "/example/", Handler: okHandler(nil)})
	s.renderHandlers = nil // the gate answers in plain text
	s.accessSettings = func(context.Context) (siteaccess.Settings, error) {
		return siteaccess.Settings{Mode: siteaccess.ModeMaintenance}, nil
	}
	h := s.dispatcher()
	for _, p := range []string{"/scim%2F..%2Fx", "/example%2Fx", "/scim%2fx", "/scim/a%2Fb", "/scim/../x", "//scim/x"} {
		rec := moduleDo(h, "GET", p, "203.0.113.30:1000", nil)
		if rec.Body.String() == "module" || rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: want the site gate (503), got %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if rec := moduleDo(h, "GET", "/scim/x", "203.0.113.30:1000", nil); rec.Code != 200 || rec.Body.String() != "module" {
		t.Errorf("/scim/x: %d %q", rec.Code, rec.Body.String())
	}
}

// The scanner exemption for a module under a probe name (/auth/) follows the
// same rule: an encoded slash or unclean path is not the module's.
func TestModuleProbeExemptionUsesEscapedCleanPath(t *testing.T) {
	_, s := moduleServer(t, modhost.Route{Module: "sso", Prefix: "/auth/sso/", Handler: okHandler(nil)})
	if _, passed := probeThroughIntercept(s, "/auth/sso/x"); !passed {
		t.Error("/auth/sso/x must pass")
	}
	for _, p := range []string{"/auth/sso%2F..%2Fx", "/auth%2Fsso/x", "/auth/sso/../x"} {
		if _, passed := probeThroughIntercept(s, p); passed {
			t.Errorf("%s passed the probe check", p)
		}
	}
}
