package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/redirect"
)

const captureGPTUA = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.1; +https://openai.com/gptbot"

type captureStore struct {
	mu   sync.Mutex
	hits []agents.Hit
}

func (c *captureStore) InsertHits(_ context.Context, hits []agents.Hit) error {
	c.mu.Lock()
	c.hits = append(c.hits, hits...)
	c.mu.Unlock()
	return nil
}

func (c *captureStore) got() []agents.Hit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agents.Hit(nil), c.hits...)
}

func newCaptureServer() (*Server, *captureStore) {
	st := &captureStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{agentWriter: agents.NewWriter(st, agents.Default(), nil, logger), logger: logger}, st
}

func serve(s *Server, h http.HandlerFunc, path, ua string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "20.171.207.1")
	s.publicTenantMiddleware(h).ServeHTTP(httptest.NewRecorder(), req)
}

func TestPublicMiddlewareRecordsSitemapWithRealStatus(t *testing.T) {
	s, st := newCaptureServer()
	serve(s, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}, "/api/v1/public/sitemap-au.xml", captureGPTUA)
	serve(s, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<llms/>")) },
		"/api/v1/public/llms.txt", captureGPTUA) // no explicit WriteHeader: 200
	s.agentWriter.Close()

	hits := st.got()
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2: %+v", len(hits), hits)
	}
	byPath := map[string]agents.Hit{}
	for _, h := range hits {
		byPath[h.Path] = h
	}
	sm := byPath["/sitemap-au.xml"]
	if sm.Status != 404 || sm.Bytes != 4 || sm.AgentID != "gptbot" || sm.Source != agents.SourceBackend {
		t.Errorf("sitemap hit wrong: %+v", sm)
	}
	if sm.IP == nil || sm.IP.String() != "20.171.207.1" || sm.Method != "GET" {
		t.Errorf("sitemap ip/method wrong: %+v", sm)
	}
	if l := byPath["/llms.txt"]; l.Status != 200 || l.Bytes == 0 {
		t.Errorf("llms hit wrong: %+v", l)
	}
}

func TestPublicMiddlewareIgnoresNonFeedAPIPaths(t *testing.T) {
	s, st := newCaptureServer()
	ok := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) }
	serve(s, ok, "/api/v1/public/resolve", captureGPTUA)
	serve(s, ok, "/api/v1/public/some/x", captureGPTUA)
	serve(s, ok, "/api/v1/public/sitemap.xml", "Mozilla/5.0 Firefox/120.0") // human
	s.agentWriter.Close()
	if n := len(st.got()); n != 0 {
		t.Fatalf("recorded %d hits, want 0", n)
	}
}

func TestPublicMiddlewareWithoutWriter(t *testing.T) {
	s := &Server{}
	called := false
	serve(s, func(w http.ResponseWriter, r *http.Request) { called = true }, "/api/v1/public/sitemap.xml", captureGPTUA)
	if !called {
		t.Fatal("handler not called")
	}
}

func TestBuildAgentResolver(t *testing.T) {
	id := uuid.New()
	redirect := func(_ context.Context, p string) (int, bool) {
		if p == "/old" {
			return 302, true
		}
		return 0, false
	}
	var lookedUp []string
	lookup := func(_ context.Context, p string) (*uuid.UUID, bool, error) {
		lookedUp = append(lookedUp, p)
		switch p {
		case "/guide":
			return &id, true, nil
		case "/":
			return nil, true, nil // home: served, no entity
		case "/boom":
			return nil, false, errors.New("db down")
		}
		return nil, false, nil
	}
	r := buildAgentResolver(redirect, lookup)
	ctx := context.Background()

	cases := []struct {
		path   string
		wantID *uuid.UUID
		status int
	}{
		{"/old", nil, 302},
		{"/guide", &id, 200},
		{"/guide/", &id, 200}, // trailing slash trimmed
		{"/", nil, 200},
		{"/missing", nil, 404},
		{"/boom", nil, 0},
		{"/x?y=1", nil, 0}, // query rejected
		{"relative", nil, 0},
	}
	for _, c := range cases {
		gotID, status := r(ctx, c.path)
		if status != c.status || (gotID == nil) != (c.wantID == nil) || (gotID != nil && *gotID != *c.wantID) {
			t.Errorf("%s: got (%v, %d), want (%v, %d)", c.path, gotID, status, c.wantID, c.status)
		}
	}
	for _, p := range lookedUp {
		if p == "/old" {
			t.Error("redirect must short-circuit before the entity lookup")
		}
	}
}

func TestAgentWriterCloseAfterServerShutdownOrder(t *testing.T) {
	s, _ := newCaptureServer()
	s.agentWriter.Close()
	done := make(chan struct{})
	go func() {
		serve(s, func(w http.ResponseWriter, r *http.Request) {}, "/api/v1/public/sitemap.xml", captureGPTUA)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("middleware blocked after writer Close")
	}
}

func TestRedirectStatusFn(t *testing.T) {
	rules := map[string]*redirect.Redirect{
		"/a": {StatusCode: 302},
		"/b": {},
	}
	fn := redirectStatusFn(func(_ context.Context, p string) *redirect.Redirect { return rules[p] })
	for p, want := range map[string]int{"/a": 302, "/b": 301} {
		if got, ok := fn(context.Background(), p); !ok || got != want {
			t.Errorf("%s = %d,%v want %d", p, got, ok, want)
		}
	}
	if _, ok := fn(context.Background(), "/none"); ok {
		t.Error("unexpected redirect")
	}
}

// On a blank site nothing claims "/", and resolveEntity reports not-served.
// The root must still be a 200.
func TestBuildAgentResolverRootWithoutEntity(t *testing.T) {
	lookup := func(_ context.Context, p string) (*uuid.UUID, bool, error) {
		return nil, false, nil
	}
	r := buildAgentResolver(nil, lookup)
	for _, p := range []string{"/"} {
		if id, status := r(context.Background(), p); status != 200 || id != nil {
			t.Errorf("%s: got (%v, %d), want (nil, 200)", p, id, status)
		}
	}
	if _, status := r(context.Background(), "/missing"); status != 404 {
		t.Errorf("/missing: got %d, want 404", status)
	}
}
