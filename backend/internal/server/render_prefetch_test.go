package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/render"
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

func TestAdminAddressChangePurgesRenderedPages(t *testing.T) {
	h, s, _ := swapServer(t)
	render.SetAdminPrefix(s.adminPathValue) // setupRender does this in a running server
	t.Cleanup(func() { render.SetAdminPrefix(nil) })
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.renderHandlers.cache = c
	s.renderHandlers.cacheTTL = time.Hour
	s.infra = &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, s.logger)}
	ctx := context.Background()
	key := cache.RenderedPageKey("v", "/about")
	if err := c.Set(ctx, key, []byte("old"), time.Hour); err != nil {
		t.Fatal(err)
	}

	if body := httptest.NewRecorder(); true {
		h.ServeHTTP(body, httptest.NewRequest("GET", "/", nil))
		if !strings.Contains(body.Body.String(), `"`+swapStart+`/*"`) {
			t.Fatalf("page lacks the current admin prefix in its rules:\n%s", body.Body.String())
		}
	}

	next := strongCandidate(t, s)
	if _, err := (&serverAddress{s: s}).Apply(ctx, admin.ApplyRequest{Path: next, By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(ctx, key); v != nil {
		t.Error("cached page survived the address change")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"`+next+`/*"`) || strings.Contains(body, `"`+swapStart+`/*"`) {
		t.Errorf("page after the change does not carry the new prefix:\n%s", body)
	}
}
