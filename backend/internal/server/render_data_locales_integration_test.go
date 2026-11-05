//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// Template data funcs on a multi-locale site list shared (und) and default
// locale content and never another locale's; on a single-locale site the SQL
// scope stays unset and the site's own content is listed.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderDataLocales
func TestRenderDataLocales_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping render data locales integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "rdl-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Render Data IT")
	t.Setenv("PUBLIC_BASE_URL", "https://rdl-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
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

	sfx := "x" + uuid.NewString()[:5]
	schema := "rdl-" + sfx
	schemaID, termID := uuid.New(), uuid.New()
	termSlug, attrValue := "rdl-term-"+sfx, "rdl-value-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schema, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,'tag',$2,$2)`, termID, termSlug)
	for _, locale := range []string{"und", "en", "fr"} {
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,'published',now(),$5::jsonb)`, id, schemaID, "item-"+locale, locale, `{"category":"`+attrValue+`"}`)
		exec(`INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1,$2)`, id, termID)
	}

	var prev struct {
		name, lang, xdef string
		locales          []byte
		prefixAll        bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef) == nil
	setLocales := func(locales string) {
		exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Render Data IT', 'en', $1::jsonb, false)
			ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = false, homepage_entity_id = NULL`, locales)
	}
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_terms WHERE term_id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	tests := []struct {
		name    string
		locales string
		want    []string // item names every func must see
		scoped  bool
	}{
		{"multi-locale", `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]`, []string{"item-en", "item-und"}, true},
		{"single locale", `[{"tag":"en","segment":""}]`, []string{"item-en"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLocales(tt.locales)
			// A fresh server per case: the URL registry caches the locale set.
			d := newRenderData(New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil))))

			c, cancel := d.ctx()
			scoped := entity.VisibleLocales(c) != nil
			cancel()
			if scoped != tt.scoped {
				t.Errorf("scoped = %v, want %v", scoped, tt.scoped)
			}

			check := func(fn string, got []string) {
				t.Helper()
				slices.Sort(got)
				if !slices.Equal(got, tt.want) {
					t.Errorf("%s = %v, want %v", fn, got, tt.want)
				}
			}
			var names []string
			for _, it := range d.Recent(schema, 10) {
				names = append(names, it.Name)
			}
			check("Recent", names)
			names = nil
			for _, it := range d.Query(render.QuerySpec{Schema: schema, Limit: 10, Page: 1}).Items {
				names = append(names, it.Name)
			}
			check("Query", names)

			var terms []render.TermView
			for _, tv := range d.Terms("tag", 500, false) {
				if tv.Slug == termSlug {
					terms = append(terms, tv)
				}
			}
			if len(terms) != 1 || terms[0].Count != len(tt.want) {
				t.Errorf("Terms = %+v, want one term counting %d entities", terms, len(tt.want))
			}
			var attrs []render.AttrCount
			for _, a := range d.AttrValues(schema, "category", 50) {
				if a.Value == attrValue {
					attrs = append(attrs, a)
				}
			}
			if len(attrs) != 1 || attrs[0].Count != len(tt.want) {
				t.Errorf("AttrValues = %+v, want one value counting %d entities", attrs, len(tt.want))
			}
			if d.Failures() != 0 {
				t.Errorf("failures = %d", d.Failures())
			}
		})
	}
}
