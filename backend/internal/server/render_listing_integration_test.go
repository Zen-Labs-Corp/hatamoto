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

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Archives, taxonomy, author and search pages end to end:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderListings
func TestRenderListingsIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping listing integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "listing-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Listing IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Listing IT")
	t.Setenv("PUBLIC_BASE_URL", "https://listing-it.example.test")
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
	base := "/lit-" + sfx
	schemaID, shadowSchemaID := uuid.New(), uuid.New()
	termCat, termTag := uuid.New(), uuid.New()
	catSlug, tagSlug, authorName := "news-"+sfx, "go-"+sfx, "Ada Lovelace"
	authorSlug := "ada-lovelace"

	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Lit Posts',$2,'{"type":"object"}',$3)`,
		schemaID, "lit-"+sfx, fmt.Sprintf(`{"url_pattern":"%s/{slug}","archive_path":"%s","archive_title":"Listing posts"}`, base, base))
	// Entities of this schema live at /tag/{slug}: they must beat the built-in tag page.
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Lit Shadow',$2,'{"type":"object"}',$3)`,
		shadowSchemaID, "lit-shadow-"+sfx, `{"url_pattern":"/tag/{slug}"}`)
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,'category',$2,'News IT'),($3,'tag',$4,'Go IT')`, termCat, catSlug, termTag, tagSlug)
	// The byline is a public person (entities reference it by byline_person_id).
	authorID := uuid.New()
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1, id, $3::text, $2, 'published', now(), '{"public":true,"short_bio":"Wrote the first program."}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug = 'person'`, authorID, authorSlug, authorName)
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, authorID) })

	const posts = 14 // 12 per page: two pages
	var firstSlug string
	for i := 1; i <= posts; i++ {
		id := uuid.New()
		slug := fmt.Sprintf("post-%d", i)
		if i == 1 {
			firstSlug = slug
		}
		content := fmt.Sprintf(`<p>Body of post %d.</p>`, i)
		if i == 3 {
			content = `<p>The zebra crossed the road.</p><script>alert('x')</script>`
		}
		// Newest first by number: post-14 is the newest.
		// Posts are bylined by attributes.author; the creating account (none here) never is.
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,byline_person_id)
			VALUES ($1,$2,$3,$4,'published', now() - ($5 || ' hours')::interval,$6::jsonb,$7)`,
			id, schemaID, fmt.Sprintf("Post number %d", i), slug, fmt.Sprint(posts-i),
			fmt.Sprintf(`{"content":%q,"author":%q}`, content, authorName), authorID)
		if i <= 3 {
			exec(`INSERT INTO hatamoto_entity_terms (entity_id,term_id) VALUES ($1,$2),($1,$3)`, id, termCat, termTag)
		}
	}
	// An unpublished post and a noindex post must never be listed or found.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		VALUES ($1,$2,'Draft platypus','draft-p','draft','{"content":"<p>platypus</p>"}')`, uuid.New(), schemaID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Hidden narwhal','hidden-n','published',now(),'{"content":"<p>narwhal</p>","no_index":true}')`, uuid.New(), schemaID)
	// Other noindex spellings: attributes.seo.robots and metadata.no_index.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Robots manatee','robots-m','published',now(),'{"content":"<p>manatee</p>","seo":{"robots":"noindex, follow"}}')`, uuid.New(), schemaID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,metadata,attributes)
		VALUES ($1,$2,'Meta okapi','meta-o','published',now(),'{"no_index":true}','{"content":"<p>okapi</p>"}')`, uuid.New(), schemaID)
	// A schema with no archive (a page-like schema) is never listed under an author,
	// even when its entities carry the same author attribute.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,byline_person_id)
		VALUES ($1,$2,'Shadow entity',$3,'published',now(),$4::jsonb,$5)`, uuid.New(), shadowSchemaID, tagSlug,
		fmt.Sprintf(`{"content":"<p>SHADOWWINS</p>","author":%q}`, authorName), authorID)
	// A 5 MB body must neither fail the insert (tsvector size limit) nor break search.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Huge post','huge-p','published', now() - interval '1000 hours',
		jsonb_build_object('content', '<p>giraffe ' || (SELECT string_agg('w' || g, ' ') FROM generate_series(1, 700000) g) || '</p>'))`, uuid.New(), schemaID)

	var hadSite bool
	_ = db.Pool().QueryRow(ctx, `SELECT true FROM hatamoto_site_config`).Scan(&hadSite)
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Listing IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, sid := range []uuid.UUID{schemaID, shadowSchemaID} {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, sid)
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, sid)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE id IN ($1,$2)`, termCat, termTag)
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(path string) (int, string, http.Header) {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String(), rec.Header()
	}
	mustGet := func(path string, wantStatus int) string {
		t.Helper()
		code, body, _ := get(path)
		if code != wantStatus {
			t.Fatalf("GET %s = %d, want %d\n%s", path, code, wantStatus, body)
		}
		return body
	}
	count := func(body string) int { return strings.Count(body, `class="hm-card"`) }

	// A post that only has attributes.content renders, sanitised, with its author and terms.
	body := mustGet(base+"/"+firstSlug, 200)
	for _, want := range []string{"<h1 class=\"hm-article__title\">Post number 1</h1>", "<p>Body of post 1.</p>",
		`href="/author/` + authorSlug + `"`, `href="/category/` + catSlug + `"`, `href="/tag/` + tagSlug + `"`, `datetime="`} {
		if !strings.Contains(body, want) {
			t.Errorf("single: missing %q\n%s", want, body)
		}
	}
	if strings.Contains(mustGet(base+"/post-3", 200), "alert(") {
		t.Error("single: script survived the render-time sanitiser")
	}

	// No saved theme colours: nothing may pin the light palette inline, or dark
	// mode (data-theme="dark" in the stylesheet) would be overridden.
	if body := mustGet(base, 200); strings.Contains(body, "--hm-bg") || strings.Contains(body, "--hm-text") {
		t.Errorf("default site must not emit inline colour tokens\n%s", body)
	}

	// Archive: page 1 has 12 cards, rel=next and an absolute canonical.
	body = mustGet(base, 200)
	if n := count(body); n != 12 {
		t.Errorf("archive page 1: %d cards, want 12", n)
	}
	for _, want := range []string{"Listing posts", `<link rel="next" href="https://listing-it.example.test` + base + `?page=2">`,
		`<link rel="canonical" href="https://listing-it.example.test` + base + `">`, `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("archive page 1: missing %q", want)
		}
	}
	if strings.Contains(body, `rel="prev"`) || strings.Contains(body, "Draft platypus") {
		t.Error("archive page 1: unexpected prev link or draft")
	}
	if i14, i13 := strings.Index(body, "Post number 14"), strings.Index(body, "Post number 13"); i14 < 0 || i13 < i14 {
		t.Error("archive: newest post must come first")
	}
	// Page 2: the 15th published row (the noindex post is only hidden from search).
	body = mustGet(base+"?page=2", 200)
	if n := count(body); n != 6 {
		t.Errorf("archive page 2: %d cards, want 6 (18 published rows - 12)", n)
	}
	for _, want := range []string{`<link rel="prev" href="https://listing-it.example.test` + base + `">`,
		`<link rel="canonical" href="https://listing-it.example.test` + base + `?page=2">`} {
		if !strings.Contains(body, want) {
			t.Errorf("archive page 2: missing %q\n%s", want, body)
		}
	}
	// A page past the end is a 404; garbage falls back to page 1.
	mustGet(base+"?page=999", 404)
	mustGet(base+"?page=abc", 200)

	// Taxonomy: three posts carry the category and the tag.
	for _, p := range []string{"/category/" + catSlug, "/tag/" + tagSlug + "-nope"} {
		code, body, _ := get(p)
		switch {
		case strings.HasSuffix(p, "-nope"):
			if code != 404 {
				t.Errorf("GET %s = %d, want 404", p, code)
			}
		case code != 200 || count(body) != 3 || !strings.Contains(body, "News IT"):
			t.Errorf("GET %s = %d with %d cards\n%s", p, code, count(body), body)
		}
	}

	// A schema entity at /tag/{slug} wins over the built-in tag listing.
	if body := mustGet("/tag/"+tagSlug, 200); !strings.Contains(body, "SHADOWWINS") || count(body) != 0 {
		t.Errorf("/tag/%s: entity must shadow the built-in page\n%s", tagSlug, body)
	}

	// Author: listed by slugified name; email never appears.
	body = mustGet("/author/"+authorSlug, 200)
	// Exactly the 14 bylined posts: not the page-schema entity with the same author attribute.
	if count(body) != 12 || !strings.Contains(body, authorName) || !strings.Contains(body, "14 posts") || strings.Contains(body, "SHADOWWINS") {
		t.Errorf("author page: %d cards\n%s", count(body), body)
	}
	if body := mustGet("/author/"+authorSlug+"?page=2", 200); count(body) != 2 {
		t.Errorf("author page 2: %d cards, want 2", count(body))
	}
	mustGet("/author/nobody-"+sfx, 404)

	// Search: full text over content, drafts and noindex entities excluded.
	body = mustGet("/search?q=zebra", 200)
	if count(body) != 1 || !strings.Contains(body, "Post number 3") || !strings.Contains(body, `name="robots" content="noindex`) {
		t.Errorf("search zebra: %d cards\n%s", count(body), body)
	}
	for _, q := range []string{"platypus", "narwhal", "manatee", "okapi", "nosuchword" + sfx, "-zebra"} {
		if body := mustGet("/search?q="+q, 200); count(body) != 0 || !strings.Contains(body, "No results for") || strings.Contains(body, "Nothing here yet") || strings.Contains(body, "0 results") {
			t.Errorf("search %q: %d cards, want one \"No results for\" line", q, count(body))
		}
	}
	// The 5 MB row was indexed (truncated) and is found by an early word.
	if body := mustGet("/search?q=giraffe", 200); count(body) != 1 || !strings.Contains(body, "Huge post") {
		t.Errorf("search giraffe: %d cards", count(body))
	}
	// Malformed queries never 500.
	for _, q := range []string{"%FF", "%00", "a%00b%FFc", strings.Repeat("zebra%20", 400)} {
		mustGet("/search?q="+q, 200)
	}
	if body := mustGet("/search?q=%22%20OR%20%28", 200); count(body) != 0 { // websearch syntax never errors
		t.Errorf("search punctuation: %d cards", count(body))
	}
	if body := mustGet("/search", 200); !strings.Contains(body, `role="search"`) || count(body) != 0 {
		t.Error("search without q: expected the empty form")
	}
	body = mustGet("/search?q=post", 200)
	if !strings.Contains(body, `?q=post&amp;page=2`) && !strings.Contains(body, `?page=2&amp;q=post`) {
		t.Errorf("search pagination must keep q:\n%s", body)
	}

	// ?type= limits a search to one schema (or an alias); unknown or malformed
	// types find nothing instead of searching the whole site.
	if body := mustGet("/search?q=zebra&type=lit-"+sfx, 200); count(body) != 1 || !strings.Contains(body, `name="type" value="lit-`+sfx+`"`) {
		t.Errorf("search zebra in the post schema: %d cards\n%s", count(body), body)
	}
	for _, typ := range []string{"lit-shadow-" + sfx, "nosuchtype", "A%20B", "..%2Fx"} {
		if body := mustGet("/search?q=zebra&type="+typ, 200); count(body) != 0 || !strings.Contains(body, "No results for") {
			t.Errorf("search zebra with type %q: %d cards, want none", typ, count(body))
		}
	}
	body = mustGet("/search?q=post&type=lit-"+sfx, 200)
	if !strings.Contains(body, `type=lit-`+sfx) || !strings.Contains(body, "page=2") {
		t.Errorf("search pagination must keep type:\n%s", body)
	}
	if body := mustGet("/search?type=lit-"+sfx, 200); !strings.Contains(body, `role="search"`) || count(body) != 0 || !strings.Contains(body, `name="type"`) {
		t.Error("type without q: expected the empty form that keeps the type")
	}
}
