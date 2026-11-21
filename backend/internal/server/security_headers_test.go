package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
)

func headerServer(enforce, production, disabled bool) *Server {
	cfg := &config.Config{}
	cfg.Security.CSPEnforce = enforce
	cfg.Security.HeadersDisabled = disabled
	if production {
		cfg.Environment = "production"
	}
	return &Server{cfg: cfg}
}

func TestSecurityHeadersMiddlewareMatchesBaseSet(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ enforce, production, disabled bool }{
		{false, false, false}, {true, false, false}, {false, true, false}, {true, true, false}, {true, true, true},
	} {
		s := headerServer(c.enforce, c.production, c.disabled)
		rec := httptest.NewRecorder()
		s.securityHeadersMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		want := s.baseSecurityHeaders()
		if c.disabled != (want == nil) || len(rec.Header()) != len(want) {
			t.Fatalf("%+v: %d headers set, base set has %d", c, len(rec.Header()), len(want))
		}
		for _, h := range want {
			if got := rec.Header().Get(h.Name); got != h.Value {
				t.Errorf("%+v: %s = %q, want %q", c, h.Name, got, h.Value)
			}
		}
		hd := rec.Header()
		if !c.disabled {
			if hd.Get("X-Frame-Options") != "DENY" || hd.Get(s.cspHeaderName()) != apiCSP {
				t.Errorf("%+v: unexpected literals %v", c, hd)
			}
			if (hd.Get("Strict-Transport-Security") != "") != c.production {
				t.Errorf("%+v: HSTS = %q", c, hd.Get("Strict-Transport-Security"))
			}
		}
	}
}

func headerValue(hs []secHeader, name string) string {
	for _, h := range hs {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

func TestStaticPageHeaders(t *testing.T) {
	t.Parallel()
	s := headerServer(true, true, false)
	got := s.staticPageHeaders(context.Background(), "")
	if headerValue(got, "X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", headerValue(got, "X-Frame-Options"))
	}
	if csp := headerValue(got, "Content-Security-Policy"); csp != renderCSP {
		t.Errorf("CSP = %q, want the page CSP", csp)
	}
	if headerValue(got, "Strict-Transport-Security") == "" {
		t.Error("production must keep HSTS")
	}

	const live = "https://live.example.com"
	csp := headerValue(s.staticPageHeaders(context.Background(), live), "Content-Security-Policy")
	for _, d := range []string{"script-src", "connect-src", "form-action"} {
		if !strings.Contains(directive(csp, d), live) {
			t.Errorf("%s lacks the live origin: %q", d, csp)
		}
	}
	if strings.Contains(directive(csp, "img-src"), live) || strings.Contains(renderCSP, live) {
		t.Errorf("live origin leaked into other directives: %q", csp)
	}

	report := headerServer(false, false, false).staticPageHeaders(context.Background(), "")
	if headerValue(report, "Content-Security-Policy") != "" || headerValue(report, "Content-Security-Policy-Report-Only") != renderCSP {
		t.Errorf("report-only mode: %v", report)
	}
	if headerServer(false, false, true).staticPageHeaders(context.Background(), live) != nil {
		t.Error("disabled headers must give none")
	}
}

func directive(csp, name string) string {
	for _, p := range strings.Split(csp, "; ") {
		if strings.HasPrefix(p, name+" ") {
			return p
		}
	}
	return ""
}
