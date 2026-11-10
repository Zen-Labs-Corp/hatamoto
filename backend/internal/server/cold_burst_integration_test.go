//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// coldBurstEnv is a server over a pool of four connections with a published
// blog, behind a real listener.
func coldBurstEnv(t *testing.T, bound string) (*wireEnv, *httptest.Server) {
	t.Helper()
	e := newWireEnv(t, map[string]string{
		"DATABASE_MAX_CONNECTIONS":        "4",
		"DATABASE_MIN_CONNECTIONS":        "1",
		"DATABASE_PUBLIC_ACQUIRE_TIMEOUT": bound,
	})
	s, _ := e.server(t, "/admin")
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'blog','blog','{"type":"object"}','{}')`, schemaID)
	for i := 0; i < 5; i++ {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,origin)
			VALUES ($1,$2,$3,$4,'published',now(),'{}'::jsonb,'test')`, uuid.New(), schemaID, fmt.Sprintf("Post %d", i), fmt.Sprintf("post-%d", i))
	}
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,origin)
		SELECT $1,id,'Home','home','published',now(),'{}'::jsonb,'test' FROM hatamoto_entity_schemas WHERE slug='page'`, uuid.New())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return e, ts
}

// A cold burst of public requests against a small pool is answered, each with
// a page or a 503 carrying Retry-After, and nothing queues for tens of seconds.
func TestColdBurstPublicPagesStayBounded_Integration(t *testing.T) {
	_, ts := coldBurstEnv(t, "1s")
	paths := []string{"/", "/blog", "/search", "/sitemap.xml"}
	client := &http.Client{Timeout: 20 * time.Second}

	const n = 50
	type result struct {
		status  int
		retry   string
		elapsed time.Duration
		err     error
	}
	results := make([]result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			t0 := time.Now()
			resp, err := client.Get(ts.URL + paths[i%len(paths)])
			if err != nil {
				results[i] = result{err: err, elapsed: time.Since(t0)}
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			results[i] = result{status: resp.StatusCode, retry: resp.Header.Get("Retry-After"), elapsed: time.Since(t0)}
		}()
	}
	close(start)
	wg.Wait()

	ok := 0
	var slowest time.Duration
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("request %d (%s): %v", i, paths[i%len(paths)], r.err)
		}
		slowest = max(slowest, r.elapsed)
		switch r.status {
		case http.StatusOK:
			ok++
		case http.StatusServiceUnavailable:
			if r.retry != "2" {
				t.Errorf("request %d: 503 without Retry-After: 2 (got %q)", i, r.retry)
			}
		default:
			t.Errorf("request %d (%s): status %d, want 200 or 503", i, paths[i%len(paths)], r.status)
		}
	}
	if slowest > 15*time.Second {
		t.Errorf("slowest request took %s", slowest)
	}
	if ok == 0 {
		t.Error("no request was served")
	}
	t.Logf("%d of %d served, slowest %s", ok, n, slowest)
}

// With every pool connection held, a public page answers 503 within the bound
// while admin and API requests are not given it and wait; once a connection is
// released they succeed.
func TestStarvedPoolBoundsOnlyPublicPages_Integration(t *testing.T) {
	e, ts := coldBurstEnv(t, "500ms")
	ctx := context.Background()

	// Warm what a page needs, then take every connection.
	if resp, err := http.Get(ts.URL + "/"); err == nil {
		_ = resp.Body.Close()
	}
	var held []interface{ Release() }
	for i := 0; i < 4; i++ {
		c, err := e.db.Pool().Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	released := false
	release := func() {
		if !released {
			released = true
			for _, c := range held {
				c.Release()
			}
		}
	}
	defer release()

	t0 := time.Now()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(ts.URL + "/blog/post-1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "2" {
		t.Fatalf("public page: status %d, Retry-After %q, body %.200s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
	}
	if d := time.Since(t0); d > 500*time.Millisecond+2*time.Second {
		t.Errorf("503 took %s, want about the 500ms bound", d)
	}
	if len(body) == 0 || !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Errorf("503 body is not the error page: %.200s", body)
	}

	// Admin and API are not bounded: they are still waiting well past the bound.
	for _, path := range []string{"/admin/login", "/api/v1/public/knowledge-graph"} {
		c := &http.Client{Timeout: 1500 * time.Millisecond}
		if r, err := c.Get(ts.URL + path); err == nil {
			code := r.StatusCode
			_ = r.Body.Close()
			if code == http.StatusServiceUnavailable {
				t.Errorf("%s answered 503 on a starved pool; it must not carry the bound", path)
			}
		}
	}

	release()
	for _, path := range []string{"/", "/api/v1/public/knowledge-graph"} {
		r, err := (&http.Client{Timeout: 10 * time.Second}).Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s after release: %v", path, err)
		}
		_ = r.Body.Close()
		if r.StatusCode >= 500 {
			t.Errorf("%s after release: status %d", path, r.StatusCode)
		}
	}
}
