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

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/user"
)

// The public content API against a migrated Postgres: off by default, then
// published-only, exact paging, cache validators and no leaked field.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PublicContentAPIIntegration
func TestPublicContentAPIIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "content-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Content IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Content IT")
	t.Setenv("PUBLIC_BASE_URL", "https://content-it.example.test")
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

	// ---- fixtures: an isolated, routed schema with entities in every state.
	sfx := uuid.NewString()[:6]
	schemaSlug, kind := "cit-"+sfx, "zk"+sfx
	base := "/cit-" + sfx
	schemaID, termID := uuid.New(), uuid.New()
	pubPerson, hidPerson := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, fmt.Sprintf(`{"url_pattern":"%s/{slug}","archive_path":"%s"}`, base, base))
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,$2,'news','News IT')`, termID, kind)
	person := func(id uuid.UUID, slug, name, attrs string) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			SELECT $1, id, $3::text, $2, 'published', now(), $4::jsonb FROM hatamoto_entity_schemas WHERE slug = 'person'`, id, slug, name, attrs)
	}
	person(pubPerson, "pat-"+sfx, "Pat Public", `{"public":true,"email":"pat@example.test"}`)
	person(hidPerson, "ana-"+sfx, "Ana Hidden", `{"public":false,"email":"ana@example.test","public_email":"ana-public@example.test"}`)

	type seed struct {
		slug, status, published, attrs string
		byline                         *uuid.UUID
		term                           bool
	}
	const (
		newer = "now() - interval '1 hour'"
		older = "now() - interval '2 hours'"
	)
	seeds := []seed{
		{"hello", "published", newer, `{"excerpt":"Short hello","email":"leak@example.test","seo":{"title":"Hello SEO"},"page_doc":{"x":1}}`, &hidPerson, true},
		{"world", "published", older, `{"summary":"About the world"}`, &pubPerson, false},
		{"draft", "draft", "NULL", `{}`, nil, false},
		{"later", "scheduled", "now() + interval '1 day'", `{}`, nil, false},
		{"gone", "archived", older, `{}`, nil, false},
		{"optout", "published", older, `{"seo":{"include_in_llms_txt":false}}`, nil, false},
		{"noidx", "published", older, `{"seo":{"robots":"noindex"}}`, nil, false},
	}
	for _, s := range seeds {
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,byline_person_id)
			VALUES ($1,$2,$3,$4,$5,`+s.published+`,$6::jsonb,$7)`, id, schemaID, "Entry "+s.slug, s.slug, s.status, s.attrs, s.byline)
		if s.term {
			exec(`INSERT INTO hatamoto_entity_terms (entity_id,term_id) VALUES ($1,$2)`, id, termID)
		}
	}
	// A non-public person is never listed (a published row all the same).
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1 OR id = ANY($2)`, schemaID, []uuid.UUID{pubPerson, hidPerson})
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	calls := 0
	do := func(path string, hdr map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		calls++ // a new client address per call keeps the rate limit out of the way
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public"+path, nil)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:5000", calls%250+1)
		if calls > 250 {
			req.RemoteAddr = fmt.Sprintf("203.0.113.%d:5000", calls%250+1)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	notFound := func(path string) {
		t.Helper()
		if rec := do(path, nil, nil); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404 (%s)", path, rec.Code, rec.Body)
		}
	}
	listPath, termsPath := "/content/"+schemaSlug, "/terms/"+kind

	// 1) The switch is off by default: all three routes read as unknown.
	for _, p := range []string{listPath, listPath + "/hello", termsPath} {
		if rec := do(p, nil, nil); rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
			t.Errorf("off: GET %s = %d %s", p, rec.Code, rec.Body)
		}
	}

	// 2) Switch on (the singleton row may not exist on a bare database).
	var rows int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_site_config`).Scan(&rows)
	if rows == 0 {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Content IT')`)
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_site_config`) })
	}
	if err := srv.siteConfig.SetPublicContentAPI(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.siteConfig.SetPublicContentAPI(context.Background(), false) })

	type list struct {
		Data []map[string]any `json:"data"`
		Meta map[string]any   `json:"meta"`
	}
	decode := func(rec *httptest.ResponseRecorder) list {
		t.Helper()
		var l list
		if err := jsonx.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatalf("decode: %v\n%s", err, rec.Body)
		}
		return l
	}
	slugs := func(l list) []string {
		var out []string
		for _, it := range l.Data {
			out = append(out, it["slug"].(string))
		}
		return out
	}

	rec := do(listPath, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	l := decode(rec)
	if got := strings.Join(slugs(l), ","); got != "hello,world" {
		t.Errorf("list slugs = %s, want hello,world (published only, newest first)", got)
	}
	if l.Meta["total"] != float64(2) || l.Meta["page"] != float64(1) || l.Meta["per_page"] != float64(20) || l.Meta["total_pages"] != float64(1) || l.Meta["has_next"] != false {
		t.Errorf("meta = %v", l.Meta)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=60, s-maxage=60" || !strings.HasPrefix(rec.Header().Get("ETag"), `W/"`) {
		t.Errorf("headers = %v", rec.Header())
	}

	hello := l.Data[0]
	if hello["schema"] != schemaSlug || hello["title"] != "Entry hello" || hello["excerpt"] != "Short hello" ||
		hello["url"] != "https://content-it.example.test"+base+"/hello" {
		t.Errorf("hello = %v", hello)
	}
	if a, _ := hello["author"].(map[string]any); a["name"] != "Ana Hidden" || a["slug"] != nil {
		t.Errorf("hello author = %v, want the name only for a non-public person", hello["author"])
	}
	if terms, _ := hello["terms"].([]any); len(terms) != 1 || terms[0].(map[string]any)["kind"] != kind || terms[0].(map[string]any)["slug"] != "news" || terms[0].(map[string]any)["name"] != "News IT" {
		t.Errorf("hello terms = %v", hello["terms"])
	}
	if a, _ := l.Data[1]["author"].(map[string]any); a["name"] != "Pat Public" || a["slug"] != "pat-"+sfx {
		t.Errorf("world author = %v, want name and slug of a public person", l.Data[1]["author"])
	}
	if terms, ok := l.Data[1]["terms"].([]any); !ok || len(terms) != 0 {
		t.Errorf("world terms = %v, want []", l.Data[1]["terms"])
	}

	// Paging.
	l = decode(do(listPath+"?per_page=1", nil, nil))
	if l.Meta["total"] != float64(2) || l.Meta["total_pages"] != float64(2) || l.Meta["has_next"] != true || len(l.Data) != 1 {
		t.Errorf("per_page=1 meta = %v items %d", l.Meta, len(l.Data))
	}
	l = decode(do(listPath+"?per_page=1&page=2", nil, nil))
	if l.Meta["has_next"] != false || strings.Join(slugs(l), ",") != "world" {
		t.Errorf("page 2 = %v %v", l.Meta, slugs(l))
	}
	if l = decode(do(listPath+"?per_page=500", nil, nil)); l.Meta["per_page"] != float64(100) {
		t.Errorf("per_page=500 meta = %v", l.Meta)
	}
	for _, q := range []string{"per_page=abc", "page=0", "limit=5"} {
		if rec := do(listPath+"?"+q, nil, nil); rec.Code != 400 {
			t.Errorf("?%s = %d, want 400", q, rec.Code)
		}
	}

	// Single entries: published ones answer, everything else is a 404.
	rec = do(listPath+"/hello", nil, nil)
	var one struct {
		Data map[string]any `json:"data"`
		Meta any            `json:"meta"`
	}
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &one); err != nil || rec.Code != 200 || one.Data["slug"] != "hello" || one.Meta != nil {
		t.Errorf("single hello = %d %v %s", rec.Code, err, rec.Body)
	}
	for _, slug := range []string{"draft", "later", "gone", "optout", "noidx", "missing"} {
		notFound(listPath + "/" + slug)
	}
	notFound("/content/nope-" + sfx)
	notFound("/content/person/ana-" + sfx) // a non-public person
	if rec := do("/content/person", nil, nil); strings.Contains(rec.Body.String(), "ana-"+sfx) {
		t.Errorf("person list shows a non-public person: %s", rec.Body)
	}

	// Terms.
	tl := decode(do(termsPath, nil, nil))
	if len(tl.Data) != 1 || tl.Data[0]["slug"] != "news" || tl.Data[0]["count"] != float64(1) || tl.Data[0]["kind"] != kind || tl.Meta["total"] != float64(1) {
		t.Errorf("terms = %v %v", tl.Data, tl.Meta)
	}
	notFound("/terms/zz" + sfx)
	notFound("/terms/Not%20A%20Kind")

	// Validators: a repeat with the tag answers 304 with both headers and no body.
	first := do(listPath, nil, nil)
	again := do(listPath, map[string]string{"If-None-Match": first.Header().Get("ETag")}, nil)
	if again.Code != 304 || again.Body.Len() != 0 || again.Header().Get("ETag") != first.Header().Get("ETag") || again.Header().Get("Cache-Control") != "public, max-age=60, s-maxage=60" {
		t.Errorf("repeat = %d body=%q headers=%v", again.Code, again.Body, again.Header())
	}

	// Nothing private in any body, and an owner sees the same bytes as anyone.
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "owner-" + sfx + "@example.test", Role: user.Role("owner"), Name: "Owner"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})
	tok, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: tok}
	for _, p := range []string{listPath, listPath + "/hello", listPath + "/world", termsPath, "/content/person"} {
		anonRec := do(p, nil, nil)
		ownerRec := do(p, map[string]string{"Authorization": "Bearer " + tok}, cookie)
		if anonRec.Body.String() != ownerRec.Body.String() || anonRec.Code != ownerRec.Code {
			t.Errorf("GET %s differs for an owner:\nanon  %d %s\nowner %d %s", p, anonRec.Code, anonRec.Body, ownerRec.Code, ownerRec.Body)
		}
		if anonRec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("GET %s sets Access-Control-Allow-Origin", p)
		}
		body := anonRec.Body.String()
		for _, bad := range []string{"@example.test", owner.ID.String(), hidPerson.String(), "created_by", "updated_by", "author_id", "byline_person_id", `"metadata"`, `"editorial"`, `"attributes"`, "page_doc", "Hello SEO"} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s leaks %q: %s", p, bad, body)
			}
		}
	}
}
