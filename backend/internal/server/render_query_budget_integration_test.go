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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil"
)

// TestBudgetRenderQueries is the number of SQL statements one cache-miss render
// issues in a warm process (page cache off: PUBLIC_BASE_URL is empty). Per
// page: one discarded warm-up GET, then the second GET of the same path is
// counted. Zero tolerance: a render that gains a query must say so.
//
//	HATAMOTO_TEST_DB_URL=... go test -tags integration -run TestBudgetRenderQueries ./internal/server -v
func TestBudgetRenderQueries(t *testing.T) {
	base := budgetDBURL(t)
	dsn := scratchMigratedDB(t, base)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("q", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "rq-it")
	t.Setenv("HATAMOTO_SITE_NAME", "RQ IT")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_WORKER", "off")
	t.Setenv("PUBLIC_BASE_URL", "")
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
	pcfg.MaxConns, pcfg.MinConns = 4, 1
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

	// Seed before New(): the URL registry snapshot is built at construction.
	// Fixed slugs and timestamps, so the data is the same on every run.
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('RQ IT') ON CONFLICT (id) DO NOTHING`)
	tag, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,origin)
		SELECT $1,id,'Home','home','published','2026-01-01T00:00:00Z','{}'::jsonb,'test' FROM hatamoto_entity_schemas WHERE slug='page'`, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("seeding the home page inserted %d rows (is the page schema seeded?)", tag.RowsAffected())
	}
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'blog','blog','{"type":"object"}','{}')`, schemaID)
	for i := 1; i <= 5; i++ {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,origin)
			VALUES ($1,$2,$3,$4,'published',$5,'{"content":"<p>Body.</p>"}'::jsonb,'test')`,
			uuid.New(), schemaID, fmt.Sprintf("Post %d", i), fmt.Sprintf("post-%d", i),
			time.Date(2026, 1, i, 0, 0, 0, 0, time.UTC))
	}

	s := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s.renderErr != nil {
		t.Fatal(s.renderErr)
	}
	t.Cleanup(func() { s.stopProbes() })

	measure := func(t *testing.T, h http.Handler, path string, want int) int {
		t.Helper()
		serve := func() int {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "198.51.100.9:5000"
			req.Header.Set("Sec-Fetch-Site", "none")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
			h.ServeHTTP(rec, req)
			return rec.Code
		}
		serve() // warm-up, discarded
		settleQueries(&counter)
		mark := counter.Count()
		if code := serve(); code != want {
			t.Fatalf("GET %s = %d, want %d", path, code, want)
		}
		settleQueries(&counter)
		for i, q := range counter.Since(mark) {
			t.Logf("%s q%d: %s", path, i+1, strings.Join(strings.Fields(q), " "))
		}
		return counter.Count() - mark
	}

	pages := []struct {
		key, path string
		want      int
	}{
		{"home", "/", 200},
		{"entity", "/blog/post-1", 200},
		{"archive", "/blog", 200},
		{"404", "/does-not-exist", 404},
	}
	for _, p := range pages {
		t.Run(p.key, func(t *testing.T) {
			n := measure(t, s.Handler(), p.path, p.want)
			budgetfile.Check(t, "server", "render_queries_"+p.key, int64(n), budgetfile.Metric{Unit: "queries"})
		})
	}

	// One extra query in the request path is counted exactly once.
	t.Run("extra_query_is_counted", func(t *testing.T) {
		inner := s.Handler()
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := db.Pool().Exec(r.Context(), "SELECT 1"); err != nil {
				t.Error(err)
			}
			inner.ServeHTTP(w, r)
		})
		want := measure(t, inner, "/blog/post-1", 200) + 1
		if n := measure(t, h, "/blog/post-1", 200); n != want {
			t.Fatalf("with one injected query: %d, want %d", n, want)
		}
	})
}

// This counter is what the budget trusts, so statements sent through a pgx.Batch
// or CopyFrom must count too (one per statement), or batching would hide queries.
func TestQueryCounterSeesBatchAndCopy(t *testing.T) {
	base := budgetDBURL(t)
	ctx := context.Background()
	pcfg, err := pgxpool.ParseConfig(scratchMigratedDB(t, base))
	if err != nil {
		t.Fatal(err)
	}
	var counter testutil.QueryCounter
	pcfg.ConnConfig.Tracer = &counter
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "CREATE TABLE rq_copy (a int)"); err != nil {
		t.Fatal(err)
	}

	mark := counter.Count()
	b := &pgx.Batch{}
	b.Queue("SELECT 1")
	b.Queue("SELECT 2")
	br := pool.SendBatch(ctx, b)
	for range 2 {
		if _, err := br.Exec(); err != nil {
			t.Fatal(err)
		}
	}
	if err := br.Close(); err != nil {
		t.Fatal(err)
	}
	if n := counter.Count() - mark; n != 2 {
		t.Errorf("2-statement batch counted %d: %v", n, counter.Since(mark))
	}

	mark = counter.Count()
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"rq_copy"}, []string{"a"}, pgx.CopyFromRows([][]any{{1}, {2}, {3}})); err != nil {
		t.Fatal(err)
	}
	if n := counter.Count() - mark; n != 1 {
		t.Errorf("CopyFrom counted %d: %v", n, counter.Since(mark))
	}
}

// budgetDBURL is DATABASE_URL, else HATAMOTO_TEST_DB_URL (make budgets passes the latter on as
// the former); the test skips without either.
func budgetDBURL(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "HATAMOTO_TEST_DB_URL"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	t.Skip("DATABASE_URL and HATAMOTO_TEST_DB_URL not set; needs Postgres")
	return ""
}

// settleQueries waits until no statement has started for 3 polls 50 ms apart
// (max 2 s), so an async query of one request cannot leak into the next window.
func settleQueries(c *testutil.QueryCounter) {
	deadline := time.Now().Add(2 * time.Second)
	last, stable := c.Count(), 0
	for stable < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if n := c.Count(); n != last {
			last, stable = n, 0
		} else {
			stable++
		}
	}
}
