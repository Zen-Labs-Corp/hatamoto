//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

// The query and attrValues data funcs against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderQuery
func TestRenderQueryIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping query integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "query-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Query IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Query IT")
	t.Setenv("PUBLIC_BASE_URL", "https://query-it.example.test")
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

	sfx := uuid.NewString()[:6]
	base := "/qit-" + sfx
	schema := "qit-" + sfx
	schemaID, termID, authorID := uuid.New(), uuid.New(), uuid.New()
	authorSlug, termSlug := "qa-"+sfx, "qt-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Q Posts',$2,'{"type":"object"}',$3)`,
		schemaID, schema, fmt.Sprintf(`{"url_pattern":"%s/{slug}","archive_path":"%s"}`, base, base))
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,'category',$2,'Q')`, termID, termSlug)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1, id, 'Quinn', $2::text, 'published', now(), '{"public":true}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug = 'person'`, authorID, authorSlug)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id=$1`, authorID)
	})

	// The URL registry reads schemas once, so the nested one exists up front.
	nested := "qn-" + sfx
	nestedID, rootID, midID := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Q Nested',$2,'{"type":"object"}','{"url_pattern":"/{path}"}')`, nestedID, nested)

	words := strings.Repeat("word ", 450)
	for i := 1; i <= 5; i++ {
		id := uuid.New()
		series := "s1"
		if i > 3 {
			series = "s2"
		}
		var author any
		if i%2 == 1 {
			author = authorID
		}
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,byline_person_id)
			VALUES ($1,$2,$3,$4,'published', now() - ($5 || ' hours')::interval,$6::jsonb,$7)`,
			id, schemaID, fmt.Sprintf("Post %d", i), fmt.Sprintf("p%d", i), fmt.Sprint(10-i),
			fmt.Sprintf(`{"series_slug":%q,"series_part":%d,"category":"c%d","content":"<p>%s</p>"}`, series, 6-i, i%2, words), author)
		if i <= 2 {
			exec(`INSERT INTO hatamoto_entity_terms (entity_id,term_id) VALUES ($1,$2)`, id, termID)
		}
	}
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		VALUES ($1,$2,'Draft','pd','draft','{"series_slug":"s1","category":"cdraft"}')`, uuid.New(), schemaID)

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := newRenderData(srv)
	titles := func(r render.QueryResult) string {
		var out []string
		for _, it := range r.Items {
			out = append(out, it.Name)
		}
		return strings.Join(out, ",")
	}

	// Default order: newest first; published only; all card fields filled.
	r := d.Query(render.QuerySpec{Schema: schema, Limit: 3, Page: 1})
	if titles(r) != "Post 5,Post 4,Post 3" || r.Total != 5 || r.Pages != 2 || r.Page != 1 {
		t.Errorf("default = %s total=%d pages=%d page=%d", titles(r), r.Total, r.Pages, r.Page)
	}
	if it := r.Items[0]; it.URL != base+"/p5" || it.WordCount != 450 || it.ReadingTime != 3 || it.Attrs["series_slug"] != "s2" || it.Author != "Quinn" || it.AuthorURL != "/author/"+authorSlug {
		t.Errorf("item = %+v", it)
	}
	if it := r.Items[1]; it.Author != "" { // Post 4 has no author row
		t.Errorf("byline of an unbylined post = %q", it.Author)
	}
	// Page 2, and a page past the end shows the last page.
	for _, page := range []int{2, 50} {
		if r = d.Query(render.QuerySpec{Schema: schema, Limit: 3, Page: page}); titles(r) != "Post 2,Post 1" || r.Page != 2 {
			t.Errorf("page %d = %s (page %d)", page, titles(r), r.Page)
		}
	}
	// Series by a numeric attribute (part = 6-i: 5,4,3 for s1 posts 1..3).
	r = d.Query(render.QuerySpec{Schema: schema, Attr: "series_slug", Eq: "s1", EqSet: true, Order: "attr:series_part", Limit: 10, Page: 1})
	if titles(r) != "Post 3,Post 2,Post 1" || r.Total != 3 {
		t.Errorf("series = %s total=%d", titles(r), r.Total)
	}
	// Author and term filters; unknown ones give nothing (never everything).
	if r = d.Query(render.QuerySpec{Schema: schema, Author: authorSlug, Order: "name", Limit: 10, Page: 1}); titles(r) != "Post 1,Post 3,Post 5" {
		t.Errorf("author = %s", titles(r))
	}
	if r = d.Query(render.QuerySpec{Schema: schema, Term: "category:" + termSlug, Order: "published_at", Limit: 10, Page: 1}); titles(r) != "Post 1,Post 2" {
		t.Errorf("term = %s", titles(r))
	}
	for _, q := range []render.QuerySpec{
		{Schema: schema, Author: "nobody-" + sfx}, {Schema: schema, Term: "category:nope-" + sfx},
		{Schema: schema, Term: "badref"}, {Schema: schema, Term: "bad kind:x"}, {Schema: "no-such-" + sfx},
	} {
		q.Limit, q.Page = 10, 1
		if r = d.Query(q); len(r.Items) != 0 || r.Total != 0 || r.Pages != 1 {
			t.Errorf("%+v = %s total=%d pages=%d", q, titles(r), r.Total, r.Pages)
		}
	}
	// eq given but empty matches the empty string only; a bool matches the
	// JSON boolean; the existence test is only for an absent eq.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Blank','pb','published', now(), '{"series_slug":"","featured":true}')`, uuid.New(), schemaID)
	if r = d.Query(render.QuerySpec{Schema: schema, Attr: "series_slug", EqSet: true, Limit: 10, Page: 1}); titles(r) != "Blank" {
		t.Errorf("empty eq = %s", titles(r))
	}
	if r = d.Query(render.QuerySpec{Schema: schema, Attr: "featured", Eq: "true", EqSet: true, Limit: 10, Page: 1}); titles(r) != "Blank" {
		t.Errorf("bool eq = %s", titles(r))
	}
	// People filter on the boolean on_team attribute; a hidden person never shows.
	teamIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, spec := range []struct{ name, attrs string }{
		{"Team Public " + sfx, `{"public":true,"on_team":true}`},
		{"Guest Public " + sfx, `{"public":true,"on_team":false}`},
		{"Team Hidden " + sfx, `{"public":false,"on_team":true}`},
	} {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			SELECT $1, id, $2::text, $3::text, 'published', now(), $4::jsonb FROM hatamoto_entity_schemas WHERE slug = 'person'`,
			teamIDs[i], spec.name, fmt.Sprintf("team-%d-%s", i, sfx), spec.attrs)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, teamIDs)
	})
	var team []string
	for _, it := range d.Query(render.QuerySpec{Schema: "person", Attr: "on_team", Eq: "true", EqSet: true, Limit: 50, Page: 1}).Items {
		if strings.HasSuffix(it.Name, sfx) {
			team = append(team, it.Name)
		}
	}
	if len(team) != 1 || team[0] != "Team Public "+sfx {
		t.Errorf("on_team query = %v, want only the public team member", team)
	}
	if r = d.Query(render.QuerySpec{Schema: schema, Attr: "series_slug", Limit: 10, Page: 1}); r.Total != 6 {
		t.Errorf("existence test total = %d, want 6", r.Total)
	}
	// Scheduled items only when asked; their URL is empty, drafts never show.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Soon','psoon','scheduled', now() + interval '3 days',
		'{"series_slug":"s9","part":7,"content":"<p>embargoed</p>","excerpt":"lede","featured_image":"/m/i.jpg","category":"c","secret":"x"}')`, uuid.New(), schemaID)
	q := render.QuerySpec{Schema: schema, Attr: "series_slug", Eq: "s9", EqSet: true, Limit: 10, Page: 1}
	if r = d.Query(q); len(r.Items) != 0 {
		t.Errorf("scheduled leaked into a published query: %s", titles(r))
	}
	q.IncludeScheduled = true
	q.Order = "attr:part"
	if r = d.Query(q); titles(r) != "Soon" || r.Items[0].PublishedAt.IsZero() || r.Items[0].Title != "Soon" || r.Items[0].Schema != schema {
		t.Fatalf("scheduled = %+v", r.Items)
	}
	// Only the date row: the filter and order keys stay in Attrs, nothing else.
	if it := r.Items[0]; it.URL != "" || it.Excerpt != "" || it.Image != "" || it.Category != "" || it.Author != "" || it.AuthorURL != "" ||
		it.WordCount != 0 || it.ReadingTime != 0 || len(it.Attrs) != 2 || it.Attrs["series_slug"] != "s9" || it.Attrs["part"] != float64(7) {
		t.Errorf("scheduled item leaks: %+v", it)
	}
	if r = d.Query(render.QuerySpec{Schema: schema, Attr: "category", Eq: "cdraft", EqSet: true, IncludeScheduled: true, Limit: 10, Page: 1}); len(r.Items) != 0 {
		t.Errorf("draft leaked: %s", titles(r))
	}

	// Nested {path} URLs come from one batched ancestor query.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, nestedID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, nestedID)
	})
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes) VALUES ($1,$2,'Root','root','published',now(),'{}')`, rootID, nestedID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,parent_id) VALUES ($1,$2,'Mid','mid','published',now(),'{}',$3)`, midID, nestedID, rootID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,parent_id) VALUES ($1,$2,'Leaf','leaf','published',now(),'{}',$3)`, uuid.New(), nestedID, midID)
	r = d.Query(render.QuerySpec{Schema: nested, Order: "name", Limit: 10, Page: 1})
	urls := map[string]string{}
	for _, it := range r.Items {
		urls[it.Name] = it.URL
	}
	if urls["Leaf"] != "/root/mid/leaf" || urls["Mid"] != "/root/mid" || urls["Root"] != "/root" {
		t.Errorf("nested urls = %v", urls)
	}

	// Distinct values with counts, drafts excluded.
	got := d.AttrValues(schema, "category", 50)
	if len(got) != 2 || got[0] != (render.AttrCount{Value: "c0", Count: 2}) || got[1] != (render.AttrCount{Value: "c1", Count: 3}) {
		t.Errorf("attrValues = %v", got)
	}
	if d.Failures() != 0 {
		t.Errorf("failures = %d", d.Failures())
	}
}
