//go:build integration

package server

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil"
)

// updateRenderMiss rewrites testdata/render_miss. The goldens are the HTML of
// the cache-miss render before the batching work: only regenerate them on a
// base that is known to render the intended output.
var updateRenderMiss = flag.Bool("update-render-miss", false, "rewrite testdata/render_miss goldens (base commit only)")

// missEnv is a server over a scratch database with a fixed, menu/image/byline
// carrying seed, and a statement counter on its pool.
type missEnv struct {
	srv     *Server
	counter *testutil.QueryCounter
}

// newMissEnv seeds before New(): the URL registry snapshot is built at
// construction. All ids, slugs and timestamps are fixed so the HTML is stable.
func newMissEnv(t *testing.T) *missEnv {
	t.Helper()
	dsn := scratchMigratedDB(t, budgetDBURL(t))
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("m", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "rm-it")
	t.Setenv("HATAMOTO_SITE_NAME", "RM IT")
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
	e := &missEnv{counter: &testutil.QueryCounter{}}
	pcfg.ConnConfig.Tracer = e.counter
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
	fixedID := func(kind, n int) uuid.UUID {
		return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%04d%08d", kind, n))
	}
	published := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Dates are rendered in the local zone: pin it so the HTML is the same everywhere.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	homeID, aboutID, personID := fixedID(1, 1), fixedID(1, 2), fixedID(2, 1)
	page := func(id uuid.UUID, name, slug string) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,updated_at,attributes,origin)
			SELECT $1,id,$2,$3,'published',$4,$4,'{"content":"<p>Body.</p>"}'::jsonb,'test'
			FROM hatamoto_entity_schemas WHERE slug='page'`, id, name, slug, published)
	}
	page(homeID, "Home", "home")
	page(aboutID, "About", "about")
	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('RM IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, homeID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,updated_at,attributes,origin)
		SELECT $1,id,'Pat Example','pat-example','published',$2,$2,'{"public":true}'::jsonb,'test'
		FROM hatamoto_entity_schemas WHERE slug='person'`, personID, published)

	addSchema := func(kind int, slug string, posts int, byline *uuid.UUID) {
		schemaID := fixedID(3, kind)
		exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
			schemaID, slug, fmt.Sprintf(`{"url_pattern":"/%[1]s/{slug}","archive_path":"/%[1]s"}`, slug))
		for i := 1; i <= posts; i++ {
			img := fmt.Sprintf("/uploads/rm-%s-%02d.jpg", slug, i)
			var by any
			if byline != nil && i == 1 {
				by = *byline
			}
			exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,updated_at,attributes,origin,byline_person_id)
				VALUES ($1,$2,$3,$4,'published',$5,$5,$6::jsonb,'test',$7)`,
				fixedID(4+kind, i), schemaID, fmt.Sprintf("Post %02d", i), fmt.Sprintf("post-%02d", i),
				published.Add(-time.Duration(i)*time.Hour),
				fmt.Sprintf(`{"content":"<p>Body.</p>","featured_image":%q}`, img), by)
			exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,width,height,variants)
				VALUES ($1,$1,'image/jpeg',1,$2,$3,2000,1500,$4::jsonb)`,
				fmt.Sprintf("rm-%s-%02d.jpg", slug, i), fmt.Sprintf("rm-%s-%02d", slug, i), "https://cdn.example.test"+img,
				`[{"size":"medium","url":"/uploads/m.jpg","width":600,"height":400},{"size":"thumbnail","url":"/uploads/t.jpg","width":150,"height":150}]`)
		}
	}
	addSchema(1, "blog", 12, &personID)
	addSchema(2, "news", 1, nil)
	addSchema(3, "notes", 0, nil)

	header, footer := fixedID(5, 1), fixedID(5, 2)
	exec(`INSERT INTO hatamoto_menus (id,name,slug,location,created_at) VALUES ($1,'Header','rm-header','header',$3),($2,'Footer','rm-footer','footer',$3)`,
		header, footer, published)
	item := func(n int, menu uuid.UUID, pos int, label, linkType string, entityID, url, schemaSlug any) {
		exec(`INSERT INTO hatamoto_menu_items (id,menu_id,label,link_type,entity_id,url,schema_slug,"position")
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, fixedID(6, n), menu, label, linkType, entityID, url, schemaSlug, pos)
	}
	item(1, header, 0, "Elsewhere", "external", nil, "https://example.test/", nil)
	item(2, header, 1, "Blog", "archive", nil, nil, "blog")
	item(3, footer, 0, "About", "entity", aboutID, nil, nil)

	e.srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e.srv.renderErr != nil {
		t.Fatal(e.srv.renderErr)
	}
	t.Cleanup(func() { e.srv.stopProbes() })
	return e
}

// missRender serves path once as a warm-up (discarded) and once more counted,
// as TestBudgetRenderQueries does; cold also drops the variants cache before
// the counted request. It returns the status, body and the counted statements.
func (e *missEnv) missRender(t *testing.T, path string, cold bool) (int, string, []string) {
	t.Helper()
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "198.51.100.9:5000"
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", "document")
		e.srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	serve()
	if cold {
		e.srv.renderData.resetVariants()
	}
	settleQueries(e.counter)
	mark := e.counter.Count()
	rec := serve()
	settleQueries(e.counter)
	return rec.Code, rec.Body.String(), e.counter.Since(mark)
}

// countStmts counts the statements matching re.
func countStmts(stmts []string, re *regexp.Regexp) int {
	n := 0
	for _, q := range stmts {
		if re.MatchString(q) {
			n++
		}
	}
	return n
}

var volatileHTML = regexp.MustCompile(`(nonce|csrf[-_a-z]*)="[^"]*"`)

// normaliseHTML strips per-request volatile attribute values only.
func normaliseHTML(s string) string {
	return volatileHTML.ReplaceAllString(s, `$1=""`)
}

// TestRenderMissGoldens pins the HTML of a cache-miss render, so batching the
// lookups behind it cannot change a byte of output.
//
//	go test -tags integration -run TestRenderMissGoldens ./internal/server [-update-render-miss]
func TestRenderMissGoldens(t *testing.T) {
	e := newMissEnv(t)
	for _, c := range []struct {
		name, path string
		cards      int // article cards expected in the page, -1 = not asserted
	}{
		{"home", "/", -1},
		{"entity", "/blog/post-01", -1},
		{"archive-12", "/blog", 12},
		{"archive-1", "/news", 1},
		{"archive-0", "/notes", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, body, stmts := e.missRender(t, c.path, true)
			if code != 200 {
				t.Fatalf("GET %s = %d", c.path, code)
			}
			got := normaliseHTML(body)
			_, body2, _ := e.missRender(t, c.path, false)
			if got != normaliseHTML(body2) {
				t.Fatalf("GET %s renders differently twice: the golden would flap", c.path)
			}
			if c.cards >= 0 {
				if n := strings.Count(got, `/post-`); n < c.cards || (c.cards == 0 && n != 0) {
					t.Errorf("%s: %d post links, want >= %d", c.name, n, c.cards)
				}
			}
			t.Logf("%s: %d statements", c.name, len(stmts))
			file := filepath.Join("testdata", "render_miss", c.name+".html")
			if *updateRenderMiss {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s HTML differs from %s (run with -update-render-miss only on the base)", c.path, file)
			}
		})
	}
}
