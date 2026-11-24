//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil"
)

// Load: one published post with 500 approved comments in a depth-5 tree (100
// threads of 5 nested comments, 50 threads per page), rendered through the
// real server handler, plus a steady stream of one approval per 10 seconds of
// simulated time. It records render time, allocations and HTML size and
// asserts counts only (never timings, which are flaky on shared hosts):
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run CommentLoad -v
func TestCommentLoadIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping comment load test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "cl-it")
	t.Setenv("HATAMOTO_SITE_NAME", "CL IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "CL IT")
	t.Setenv("PUBLIC_BASE_URL", "https://cl-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var counter testutil.QueryCounter
	pcfg.ConnConfig.Tracer = &counter
	pcfg.MaxConns, pcfg.MinConns = 6, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	sfx := uuid.NewString()[:6]
	schemaID, postID, otherID := uuid.New(), uuid.New(), uuid.New()
	postPath, otherPath := "/load-"+sfx, "/other-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "cl-"+sfx, `{"url_pattern":"/{slug}"}`)
	for id, slug := range map[uuid.UUID]string{postID: "load-" + sfx, otherID: "other-" + sfx} {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,'published',now(),'{"content":"<p>Post body.</p>"}'::jsonb)`, id, schemaID, slug)
	}
	var prevName string
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name FROM hatamoto_site_config`).Scan(&prevName) == nil
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('CL IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_comment_settings`)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	set := comment.Defaults()
	set.Enabled = true
	if err := srv.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}

	// 100 threads, each a chain of 5 (root + 4 nested replies): 500 comments, depth 5.
	store := func(entityID uuid.UUID, parent *uuid.UUID, n int) comment.Comment {
		t.Helper()
		raw := fmt.Sprintf("Comment %d: a plain paragraph of text that is long enough to be realistic, "+
			"with *emphasis* and a link https://example.test/%d in it.", n, n)
		res, err := commentfmt.Render(raw)
		if err != nil {
			t.Fatal(err)
		}
		c, err := srv.comments.Store(ctx, comment.Draft{
			EntityID: entityID, ParentID: parent, AuthorName: fmt.Sprintf("Visitor %d", n%37),
			BodyRaw: raw, BodyHTML: res.HTML, HasLinks: res.HasLinks, LinkDomains: res.LinkDomains,
			BodyHash: res.BodyHash, SpamVerdict: "allow", Status: comment.StatusApproved,
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	seedStart := time.Now()
	n := 0
	for range 100 {
		var parent *uuid.UUID
		for range 5 {
			n++
			c := store(postID, parent, n)
			parent = &c.ID
		}
	}
	store(otherID, nil, 0)
	t.Logf("seeded %d comments in %s", n, time.Since(seedStart).Round(time.Millisecond))

	get := func(path string) (*httptest.ResponseRecorder, time.Duration) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		start := time.Now()
		srv.router.ServeHTTP(rec, req)
		return rec, time.Since(start)
	}
	// threadQueries counts the thread statements run since mark. The thread
	// statement is the only one that joins the roots CTE, whatever its leading
	// WITH clauses (the per-type order is resolved in a dir CTE before it).
	threadQueries := func(mark int) int {
		c := 0
		for _, q := range counter.Since(mark) {
			if strings.Contains(q, "FROM roots r") {
				c++
			}
		}
		return c
	}
	pct := func(d []time.Duration, p float64) time.Duration {
		s := slices.Clone(d)
		slices.Sort(s)
		return s[min(len(s)-1, int(float64(len(s))*p))]
	}

	// Uncached renders: a query string is never cached, so ?cpage=1 is the
	// first page rendered from scratch every time.
	const reps = 100
	for _, tc := range []struct{ name, path string }{
		{"page 1 uncached", postPath + "?cpage=1"},
		{"page 2 uncached", postPath + "?cpage=2"},
	} {
		if rec, _ := get(tc.path); rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="hm-comments"`) {
			t.Fatalf("%s: status %d, no comments section in %.200s", tc.name, rec.Code, rec.Body)
		}
		mark := counter.Count()
		times := make([]time.Duration, 0, reps)
		var size int
		for range reps {
			rec, d := get(tc.path)
			if rec.Code != 200 {
				t.Fatalf("%s: status %d", tc.name, rec.Code)
			}
			times = append(times, d)
			size = rec.Body.Len()
		}
		if got := threadQueries(mark); got != reps {
			t.Errorf("%s: %d thread queries for %d renders, want one each", tc.name, got, reps)
		}
		allocs := testing.AllocsPerRun(20, func() { get(tc.path) })
		t.Logf("%s: p50 %s p95 %s, %.0f allocs/render, HTML %d bytes",
			tc.name, pct(times, 0.5).Round(time.Microsecond), pct(times, 0.95).Round(time.Microsecond), allocs, size)
	}

	// Cached page 1: the first request renders and stores, the rest are hits.
	mark := counter.Count()
	get(postPath)
	if got := threadQueries(mark); got != 1 {
		t.Fatalf("first cached-path render ran %d thread queries, want 1", got)
	}
	mark = counter.Count()
	times := make([]time.Duration, 0, reps)
	for range reps {
		rec, d := get(postPath)
		if rec.Code != 200 {
			t.Fatalf("cached: status %d", rec.Code)
		}
		times = append(times, d)
	}
	if got := threadQueries(mark); got != 0 {
		t.Errorf("cached page 1 ran %d thread queries, want 0", got)
	}
	t.Logf("page 1 cached: p50 %s p95 %s", pct(times, 0.5).Round(time.Microsecond), pct(times, 0.95).Round(time.Microsecond))

	// Stream: one approval per simulated 10 s while 100 reads of each page land
	// in that window. The post re-renders once per approval, the other post never.
	get(otherPath)
	const windows, readsPerWindow = 6, 100
	var simulated time.Duration
	for w := range windows {
		simulated += 10 * time.Second
		store(postID, nil, 1000+w)
		mark := counter.Count()
		for range readsPerWindow {
			if rec, _ := get(postPath); rec.Code != 200 {
				t.Fatalf("stream post: status %d", rec.Code)
			}
		}
		if got := threadQueries(mark); got != 1 {
			t.Errorf("window %d: the post ran %d thread queries, want 1 (one re-render per approval)", w, got)
		}
		mark = counter.Count()
		for range readsPerWindow {
			if rec, _ := get(otherPath); rec.Code != 200 {
				t.Fatalf("stream other post: status %d", rec.Code)
			}
		}
		if got := threadQueries(mark); got != 0 {
			t.Errorf("window %d: the other post re-rendered (%d thread queries)", w, got)
		}
	}
	t.Logf("stream: %s simulated, %d approvals, %d reads of each page, one re-render per approval, the other post never",
		simulated, windows, windows*readsPerWindow)
}
