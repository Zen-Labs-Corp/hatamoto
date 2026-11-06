package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/redirect"
)

// Only the strict policy names the keyword; the permissive one already allows
// inline script and stays as it was.
func TestRenderCSPInlineSpeculationRules(t *testing.T) {
	const kw = "'inline-speculation-rules'"
	if !strings.Contains(renderCSPStrict, "script-src 'self' "+kw+";") {
		t.Errorf("strict policy lacks the keyword: %s", renderCSPStrict)
	}
	if strings.Contains(renderCSP, kw) || strings.Contains(sandboxCSP, kw) {
		t.Error("only the strict policy carries the keyword")
	}
}

// A browser prefetch is an ordinary GET with Sec-Purpose: prefetch. The
// response is the same as for a visit; what it must not do is count as one
// (redirect hits and the 404 log, see TestPrefetchSkipsCounters). Pageviews
// come from the JS beacon, which a prefetch does not run.
func TestPrefetchRequestServedNormally(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	plain := f.get("/about")
	pre := f.get("/about", func(r *http.Request) { r.Header.Set("Sec-Purpose", "prefetch") })
	if pre.Code != http.StatusOK || pre.Body.String() != plain.Body.String() {
		t.Fatalf("prefetch: %d, body differs=%v", pre.Code, pre.Body.String() != plain.Body.String())
	}
	if !strings.Contains(pre.Body.String(), `type="speculationrules"`) {
		t.Error("page lacks the speculation rules")
	}
}

// A custom admin address is a secret: the public rules never print it, so a
// visitor cannot read it from a page (the scanner tests cover the 404s).
func TestPrefetchRulesNeverPrintCustomAdminAddress(t *testing.T) {
	h, s, _ := swapServer(t)
	for _, addr := range []string{swapStart, strongCandidate(t, s)} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		body := rec.Body.String()
		if !strings.Contains(body, `type="speculationrules"`) {
			t.Fatal("page lacks the rules block; test premise broken")
		}
		if strings.Contains(body, addr) {
			t.Errorf("public page prints the admin address %s", addr)
		}
		if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: addr, By: "Ana"}); err != nil {
			t.Fatal(err)
		}
	}
}

// A prefetch of a missing path writes no 404-log entry; a visit does.
func TestPrefetchSkipsCounters(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	for _, v := range []string{"prefetch", "prefetch;anonymous-client-ip"} {
		f.get("/gone", func(r *http.Request) { r.Header.Set("Sec-Purpose", v) })
	}
	if len(f.notFound) != 0 {
		t.Fatalf("prefetch logged 404s: %v", f.notFound)
	}
	f.get("/gone")
	if len(f.notFound) != 1 {
		t.Fatalf("a visit must log the 404: %v", f.notFound)
	}
}

// The middleware marks a prefetch request so redirect lookups do not count it.
func TestPrefetchMiddlewareMarksContext(t *testing.T) {
	var counted bool
	h := prefetchMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		counted = !redirect.IsUncounted(r.Context())
	}))
	req := httptest.NewRequest("GET", "/old", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !counted {
		t.Error("a visit must count")
	}
	req.Header.Set("Sec-Purpose", "prefetch")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if counted {
		t.Error("a prefetch must not count")
	}
}
