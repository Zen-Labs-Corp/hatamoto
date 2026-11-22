package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
	"time"
)

func llmsTestServer(settings string) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{
		logger:   logger,
		infra:    NewInfrastructure(logger),
		seoStore: seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(settings)}}),
	}
}

func fakeLLMsBuild(calls *atomic.Int32) func(context.Context, string, seo.AIFiles) (llmsResult, error) {
	return func(ctx context.Context, base string, _ seo.AIFiles) (llmsResult, error) {
		calls.Add(1)
		return llmsResult{base: base, body: []byte("# Acme Studio\n")}, nil
	}
}

func TestLLMsSwitchedOffIs404(t *testing.T) {
	for _, tc := range []struct {
		path, settings string
		full           bool
	}{
		{"/llms.txt", `{"ai_files":{"llms_off":true}}`, false},
		{"/llms-full.txt", `{"ai_files":{"full_off":true}}`, true},
	} {
		s := llmsTestServer(tc.settings)
		var calls atomic.Int32
		w := httptest.NewRecorder()
		s.serveLLMsWith(w, httptest.NewRequest(http.MethodGet, "http://acme.example"+tc.path, nil), tc.full, fakeLLMsBuild(&calls))
		if w.Code != http.StatusNotFound || calls.Load() != 0 {
			t.Fatalf("%s off: status %d, builds %d", tc.path, w.Code, calls.Load())
		}
	}
	// One file off leaves the other on.
	s := llmsTestServer(`{"ai_files":{"llms_off":true}}`)
	var calls atomic.Int32
	w := httptest.NewRecorder()
	s.serveLLMsWith(w, httptest.NewRequest(http.MethodGet, "http://acme.example/llms-full.txt", nil), true, fakeLLMsBuild(&calls))
	if w.Code != http.StatusOK {
		t.Fatalf("full with only llms off: status %d", w.Code)
	}
}

func TestLLMsHeadersAndCacheHit(t *testing.T) {
	s := llmsTestServer(`{}`)
	var calls atomic.Int32
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.serveLLMsWith(w, httptest.NewRequest(http.MethodGet, "http://acme.example/llms.txt", nil), false, fakeLLMsBuild(&calls))
		return w
	}
	first, second := get(), get()
	if calls.Load() != 1 {
		t.Fatalf("builds = %d, want 1 (second read from the render cache)", calls.Load())
	}
	for _, w := range []*httptest.ResponseRecorder{first, second} {
		if w.Code != http.StatusOK || w.Body.String() != "# Acme Studio\n" {
			t.Fatalf("status %d body %q", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
		if got := w.Header().Get("Cache-Control"); got != "public, max-age=300, s-maxage=300" {
			t.Fatalf("Cache-Control = %q", got)
		}
	}
	// A purge drops the entry, so the next read builds again.
	_ = s.infra.CacheInvalidator.InvalidateRendered(context.Background())
	get()
	if calls.Load() != 2 {
		t.Fatalf("builds after purge = %d, want 2", calls.Load())
	}
}

func TestLLMsPoolBusySkipsCache(t *testing.T) {
	s := llmsTestServer(`{}`)
	var calls atomic.Int32
	for range 2 {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://acme.example/llms.txt", nil)
		r = r.WithContext(database.WithAcquireBound(r.Context(), time.Second))
		build := fakeLLMsBuild(&calls)
		s.serveLLMsWith(w, r, false, func(ctx context.Context, base string, set seo.AIFiles) (llmsResult, error) {
			database.NotePoolBusy(r.Context()) // a bounded acquire of this request gave up mid-build
			return build(ctx, base, set)
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("builds = %d, want 2: a busy build must not be stored", calls.Load())
	}
	if body, _ := s.infra.Cache.Get(context.Background(), cache.RenderedPageKey("llms:http://acme.example", "/llms.txt")); body != nil {
		t.Fatal("busy build was cached")
	}
}

func TestLLMsDetailsFromProfile(t *testing.T) {
	p := bizprofile.Profile{
		Email: "hello@acme.example", Phone: "+351 21 000 0000",
		Address:      bizprofile.Address{City: "Lisbon", Country: "Portugal"},
		OpeningHours: []bizprofile.Hours{{Days: []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday"}, Opens: "09:00", Closes: "18:00"}, {Days: []string{"Saturday"}, Opens: "10:00", Closes: "14:00"}},
	}
	want := "Based in Lisbon, Portugal.\nhello@acme.example, +351 21 000 0000.\nOpen Mon-Fri 09:00-18:00, Sat 10:00-14:00."
	if got := llmsDetails(p); got != want {
		t.Fatalf("details:\n%s\nwant:\n%s", got, want)
	}
	// Free-text hours win over the structured ones.
	p.HoursNote = "by appointment"
	if got := llmsDetails(p); got != "Based in Lisbon, Portugal.\nhello@acme.example, +351 21 000 0000.\nOpen by appointment." {
		t.Fatalf("details with note:\n%s", got)
	}
	if got := llmsDetails(bizprofile.Profile{}); got != "" {
		t.Fatalf("empty profile gave %q", got)
	}
}

func TestLimitPostsKeepsNewestPerLocale(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	mk := func(locale, schema string, d int) llmsRef {
		return llmsRef{entry: &entity.SitemapEntry{SchemaSlug: schema, PublishedAt: day(d)}, path: "/" + locale + schema + string(rune('a'+d)), locale: locale}
	}
	refs := []llmsRef{mk("en", "post", 1), mk("en", "post", 3), mk("en", "post", 2), mk("fr", "post", 1), mk("en", "page", 1)}
	got := limitPosts(refs, func(r llmsRef) bool { return r.entry.SchemaSlug == "post" }, 2)
	if len(got) != 4 { // two en posts, the fr post, the page
		t.Fatalf("kept %d refs, want 4", len(got))
	}
	for _, r := range got {
		if r.locale == "en" && r.entry.SchemaSlug == "post" && r.entry.PublishedAt.Equal(day(1)) {
			t.Fatal("the oldest en post survived the cap")
		}
	}
}
