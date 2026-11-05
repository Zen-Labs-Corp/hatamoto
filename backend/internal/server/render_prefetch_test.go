package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin"
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

// A browser prefetch is an ordinary GET with Sec-Purpose: prefetch. Server-side
// counting reads only the Referer host of an AI assistant (render_ai_referral.go,
// own-site links never match) and pageviews come from the JS beacon, which a
// prefetch does not run, so there is nothing to skip: the response is the same.
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
