//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

var updatePublicJSON = flag.Bool("update", false, "rewrite testdata/public_json goldens")

var (
	reUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// The AI crawler comment embeds the current month ("LAST VERIFIED: October 2026").
	reVerified = regexp.MustCompile(`LAST VERIFIED: [A-Za-z]+ \d{4}`)
	reTime     = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
)

// normalizePublicJSON masks volatile ids and timestamps by regex, so key
// order and every other byte of the handler output stay under test.
func normalizePublicJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	raw = reUUID.ReplaceAll(raw, []byte("<uuid>"))
	raw = reTime.ReplaceAll(raw, []byte("<time>"))
	raw = reVerified.ReplaceAll(raw, []byte("LAST VERIFIED: <month>"))
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, raw)
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// Characterization of the page loaders behind the Go renderer (homepage, page),
// captured from the former public JSON endpoints. Regenerate with
// DATABASE_URL=... go test -tags integration ./internal/server -run PublicPagesJSON -update
func TestPublicPagesJSON_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping public pages JSON characterization (needs a migrated Postgres)")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
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

	// Fixed names (goldens embed them); cleaned up before and after.
	const schemaSlug = "pj-note"
	schemaID := uuid.New()
	cleanup := func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id IN (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1)`, schemaSlug)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug = $1`, schemaSlug)
	}
	cleanup()
	t.Cleanup(cleanup)

	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/notes/{slug}","archive_path":"/notes"}`)

	sections := `{"page_sections":[
		{"id":"s-hero","type":"hero","title":"Hero","isVisible":true,"sort_order":0,"content":{"headline":"Hello","subheadline":"World"}},
		{"id":"s-faq","type":"faq","title":"FAQ","isVisible":true,"sort_order":1,"content":{"faqs":[{"question":"Q?","answer":"A."}]}}],
		"meta_description":"A described page","og_image":"/media/og.png"}`
	ins := func(slug string, attrs string) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,'published',now(),$4::jsonb)`, uuid.New(), schemaID, slug, attrs)
	}
	ins("home", sections)
	ins("pj-page", sections)
	ins("pj-noindex", `{"no_index":true}`)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	entitySvc := entity.NewService(entity.NewPostgresSchemaRepository(db), nil, logger)
	srv := &Server{db: db, entityService: entitySvc, siteConfig: siteconfig.NewStore(db, slog.Default()), logger: logger}

	// The loaders feed the Go renderer; marshal their result the way the removed
	// JSON endpoints did so the goldens keep covering JSON-LD, breadcrumbs,
	// the AI comment byte for byte.
	req := func(target string) *http.Request {
		return httptest.NewRequest(http.MethodGet, target, nil)
	}
	marshal := func(t *testing.T, v any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	page := func(t *testing.T) []byte {
		res, out, err := srv.loadPublicPage(req("/pj-page"), "pj-page")
		if err != nil || out.NotFound || res == nil {
			t.Fatalf("loadPublicPage: %v %+v", err, out)
		}
		return marshal(t, res)
	}
	home := func(t *testing.T) []byte {
		res, out, err := srv.loadHomepage(req("/"))
		if err != nil || out.NotFound || res == nil {
			t.Fatalf("loadHomepage: %v %+v", err, out)
		}
		return marshal(t, res)
	}
	for _, tc := range []struct {
		golden string
		load   func(*testing.T) []byte
	}{
		{"homepage.json", home},
		{"page.json", page},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			got := normalizePublicJSON(t, tc.load(t))
			path := filepath.Join("testdata", "public_json", tc.golden)
			if *updatePublicJSON {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s drifted from golden.\n got: %s\nwant: %s", tc.golden, got, want)
			}
		})
	}

	t.Run("noindex", func(t *testing.T) {
		res, out, err := srv.loadPublicPage(req("/pj-noindex"), "pj-noindex")
		if err != nil || out.NotFound || res == nil {
			t.Fatalf("noindex page: %v %+v", err, out)
		}
		if !res.Meta.NoIndex {
			t.Errorf("meta.no_index = false, want true: %s", marshal(t, res))
		}
	})
	// The frozen page_sections mirror never reaches the JSON: it is derived from
	// page_doc (absent for a builder-only doc).
	t.Run("derived sections", func(t *testing.T) {
		comp := `{"id":"k","t":"component","p":{"type":"content","content":{"body":"fresh"}}}`
		v1 := `{"v":2,"nodes":[{"id":"s","t":"container","p":{"tag":"section","v1":true},"c":[` + comp + `]}]}`
		bo := `{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"},"c":[` + comp + `]}]}`
		stale := `[{"id":"k","type":"content","sort_order":0,"content":{"body":"stale"}}]`
		ins("pj-v1doc", `{"page_doc":`+v1+`,"page_sections":`+stale+`}`)
		ins("pj-bodoc", `{"page_doc":`+bo+`,"page_sections":`+stale+`}`)
		get := func(slug string) string {
			res, out, err := srv.loadPublicPage(req("/"+slug), slug)
			if err != nil || out.NotFound || res == nil {
				t.Fatalf("%s: %v %+v", slug, err, out)
			}
			return string(res.Entity.Attributes)
		}
		if a := get("pj-v1doc"); !strings.Contains(a, "fresh") || strings.Contains(a, "stale") {
			t.Errorf("v1 doc: %s", a)
		}
		if a := get("pj-bodoc"); strings.Contains(a, "page_sections") || strings.Contains(a, "stale") {
			t.Errorf("builder-only doc: %s", a)
		}
	})
	t.Run("not found", func(t *testing.T) {
		if res, out, err := srv.loadPublicPage(req("/pj-missing"), "pj-missing"); err != nil || !out.NotFound {
			t.Fatalf("missing page: res=%v out=%+v err=%v", res, out, err)
		}
	})
}
