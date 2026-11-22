//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"flag"
	"fmt"
	"io"

	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// updateLLMsSeeded rewrites testdata/llms/seeded-*.txt.
var updateLLMsSeeded = flag.Bool("update-llms-seeded", false, "rewrite testdata/llms/seeded-*.txt")

// llmsEnv is a server over a scratch database holding a fictional two-locale
// studio site: business profile, people, services, posts, and one of each kind
// of entry that must stay out of the files.
type llmsEnv struct {
	srv  *Server
	db   *database.DB
	ids  map[string]uuid.UUID
	ctx  context.Context
	pool *pgxpool.Pool
}

type llmsOpts struct {
	access  string // site_access_mode, "" = open
	aiFiles string // the ai_files settings document, "" = defaults
	bulk    bool   // a long page, to pass a small cap
}

func newLLMsEnv(t *testing.T, o llmsOpts) *llmsEnv {
	t.Helper()
	dsn := scratchMigratedDB(t, budgetDBURL(t))
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("l", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "llms-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Acme Studio")
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
	pcfg.MaxConns, pcfg.MinConns = 4, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	e := &llmsEnv{db: db, pool: db.Pool(), ids: map[string]uuid.UUID{}, ctx: ctx}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	n := 0
	id := func(key string) uuid.UUID {
		n++
		e.ids[key] = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
		return e.ids[key]
	}
	when := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

	var schemaID = map[string]uuid.UUID{}
	for _, slug := range []string{"page", "post", "person"} {
		var sid uuid.UUID
		if err := e.pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug=$1`, slug).Scan(&sid); err != nil {
			t.Fatalf("builtin schema %s: %v", slug, err)
		}
		schemaID[slug] = sid
	}
	for slug, name := range map[string]string{"service": "Services", "recipe": "Recipes"} {
		sid := uuid.New()
		schemaID[slug] = sid
		exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$3,$2,'{"type":"object"}',$4)`,
			sid, slug, name, fmt.Sprintf(`{"url_pattern":"/%[1]s/{slug}","archive_path":"/%[1]s"}`, slug))
	}
	add := func(key, schema, name, slug, locale, status string, at time.Time, attrs string, parent *uuid.UUID) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,updated_at,attributes,origin,parent_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8::jsonb,'test',$9)`,
			id(key), schemaID[schema], name, slug, locale, status, at, attrs, parent)
	}
	day := func(d int) time.Time { return when.AddDate(0, 0, d) }
	body := func(s string) string { return fmt.Sprintf(`{"content":"<p>%s</p>"}`, s) }

	add("home", "page", "Home", "home", "en", "published", day(0), `{"content":"<p>Websites for small firms.</p>","meta_description":"Websites for small firms."}`, nil)
	add("services", "page", "Services", "services", "en", "published", day(1), `{"content":"<h2>What we build</h2><p>Brochure sites and shops.</p>","excerpt":"What we build."}`, nil)
	add("about", "page", "About", "about", "en", "published", day(2), body("We are a small studio. We like plain words."), nil)
	add("team", "page", "Our process", "process", "en", "published", day(3), body("Discovery, design, build."), &[]uuid.UUID{e.ids["about"]}[0])
	add("optout", "page", "Old pricing", "old-pricing", "en", "published", day(4), `{"content":"<p>Retired price list.</p>","seo":{"include_in_llms_txt":false}}`, nil)
	add("builder", "page", "Showcase", "showcase", "en", "published", day(5),
		`{"page_doc":{"v":2,"nodes":[{"id":"a","t":"heading","p":{"text":"Recent work","level":2}},{"id":"b","t":"text","p":{"html":"<p>Three shops and a <strong>portfolio</strong>.</p>"}},{"id":"c","t":"image","p":{"src":"https://acme.example/uploads/shop.jpg","alt":"A shop"}}]}}`, nil)
	add("draftpage", "page", "Unreleased page", "unreleased", "en", "draft", day(6), body("Not public yet."), nil)
	add("fr-home", "page", "Accueil", "accueil", "fr", "published", day(0), body("Des sites web pour les petites entreprises."), nil)

	add("web", "service", "Web design", "web-design", "en", "published", day(1), `{"content":"<p>Sites that load fast.</p>","meta_description":"Sites that load fast."}`, nil)
	add("shop", "service", "Online shops", "online-shops", "en", "published", day(2), body("Shops with simple checkout."), nil)
	add("note", "recipe", "Soup of the day", "soup", "en", "published", day(3), body("Lentils and cumin."), nil)

	add("p1", "post", "Launch notes", "launch", "en", "published", day(10), body("We opened the studio. It went well."), nil)
	add("p2", "post", "Shop checklist", "shop-checklist", "en", "published", day(20), body("Ten things to check before opening a shop."), nil)
	add("p3", "post", "Draft musings", "musings", "en", "draft", day(30), body("Unfinished."), nil)
	add("fr-post", "post", "Notes de lancement", "lancement", "fr", "published", day(10), body("Nous avons ouvert le studio."), nil)

	add("elena", "person", "Elena Voss", "elena-voss", "en", "published", day(0), `{"public":true,"job_title":"Lead designer","bio":"<p>Elena leads design.</p>"}`, nil)
	add("hidden", "person", "Hidden Person", "hidden-person", "en", "published", day(0), `{"public":false,"job_title":"Advisor"}`, nil)

	if o.bulk {
		var long strings.Builder
		for i := range 400 {
			fmt.Fprintf(&long, "<p>Paragraph %03d of the long guide: %s</p>", i, strings.Repeat("plain words ", 20))
		}
		add("long", "page", "Long guide", "long-guide", "en", "published", day(7), fmt.Sprintf(`{"content":%q}`, long.String()), nil)
	}

	// Phone, hours and address live in their own columns; the profile holds the rest.
	profile := `{"name":"Acme Studio","office":true,"email":"hello@acme.example"}`
	mode := o.access
	if mode == "" {
		mode = "open"
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, tagline, language, locales, public_base_url, homepage_entity_id, business_profile, site_access_mode, phone, business_hours, postal_address)
		VALUES ('Acme Studio', 'Acme Studio builds websites for small firms in Lisbon.', 'en', $1::jsonb, 'https://acme.example', $2, $3::jsonb, $4,
		  '+351 21 000 0000', 'Mon-Fri 9:00-18:00', '{"city":"Lisbon","country":"Portugal"}'::jsonb)
		ON CONFLICT (id) DO UPDATE SET company_name=EXCLUDED.company_name, tagline=EXCLUDED.tagline, language=EXCLUDED.language, locales=EXCLUDED.locales,
		  public_base_url=EXCLUDED.public_base_url, homepage_entity_id=EXCLUDED.homepage_entity_id, business_profile=EXCLUDED.business_profile, site_access_mode=EXCLUDED.site_access_mode,
		  phone=EXCLUDED.phone, business_hours=EXCLUDED.business_hours, postal_address=EXCLUDED.postal_address`,
		`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr","name":"Français"}]`, e.ids["home"], profile, mode)
	ai := ""
	if o.aiFiles != "" {
		ai = `,"ai_files":` + o.aiFiles
	}
	exec(`INSERT INTO hatamoto_seo_settings (id, settings) VALUES (true, $1::jsonb)
		ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings`,
		`{"index":{"noindex_types":{"recipe":true}}`+ai+`}`)
	exec(`INSERT INTO hatamoto_menus (id,name,slug,location,created_at) VALUES ($1,'Header','llms-header','header',$2)`, id("menu"), when)
	for i, k := range []string{"services", "about"} {
		exec(`INSERT INTO hatamoto_menu_items (id,menu_id,label,link_type,entity_id,"position") VALUES ($1,$2,$3,'entity',$4,$5)`,
			uuid.New(), e.ids["menu"], k, e.ids[k], i)
	}

	e.srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e.srv.renderErr != nil {
		t.Fatal(e.srv.renderErr)
	}
	t.Cleanup(func() { e.srv.stopProbes() })
	return e
}

func (e *llmsEnv) get(path string) (int, string, http.Header) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "198.51.100.9:5000"
	e.srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

func (e *llmsEnv) ok(t *testing.T, path string) string {
	t.Helper()
	code, body, h := e.get(path)
	if code != 200 {
		t.Fatalf("GET %s = %d: %.200s", path, code, body)
	}
	if ct := h.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("GET %s content type %q", path, ct)
	}
	if cc := h.Get("Cache-Control"); cc != "public, max-age=300, s-maxage=300" {
		t.Errorf("GET %s cache control %q", path, cc)
	}
	return body
}

func checkLLMsGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "llms", name)
	if *updateLLMsSeeded {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden (run with -update-llms-seeded to rewrite):\n--- got ---\n%s", name, got)
	}
}

// secrets must never appear in either file.
var llmsAbsent = []string{"Unreleased page", "Draft musings", "Old pricing", "Soup of the day", "Hidden Person", "Retired price list", "Not public yet"}

func TestLLMsSeededGoldens(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{})
	idx := e.ok(t, "/llms.txt")
	full := e.ok(t, "/llms-full.txt")
	for _, s := range llmsAbsent {
		if strings.Contains(idx, s) || strings.Contains(full, s) {
			t.Errorf("excluded text %q is in a file", s)
		}
	}
	if code, alias, _ := e.get("/api/v1/public/llms.txt"); code != 200 || alias != idx {
		t.Errorf("api alias = %d, same body %v", code, alias == idx)
	}
	checkLLMsGolden(t, "seeded-llms.txt", idx)
	checkLLMsGolden(t, "seeded-llms-full.txt", full)
}

func TestLLMsFreshAfterChange(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{})
	idx, full := e.ok(t, "/llms.txt"), e.ok(t, "/llms-full.txt")
	e.ok(t, "/llms.txt") // a second read is served from the cache
	if strings.Contains(idx, "Pricing") || strings.Contains(full, "Pricing") {
		t.Fatal("the new page is there before it exists")
	}
	var pageSchema uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug='page'`).Scan(&pageSchema); err != nil {
		t.Fatal(err)
	}
	svc := e.srv.entityService
	ent, err := svc.CreateEntity(e.ctx, nil, &entity.CreateEntityInput{SchemaID: pageSchema, Name: "Pricing", Slug: "pricing", Locale: "en",
		Attributes: jsontext.Value(`{"content":"<p>Fixed prices for small sites.</p>"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateEntityStatus(e.ctx, ent.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	e.srv.hooks.Wait() // the invalidation hooks run off the request path
	idx, full = e.ok(t, "/llms.txt"), e.ok(t, "/llms-full.txt")
	if !strings.Contains(idx, "- [Pricing](https://acme.example/pricing)") {
		t.Errorf("llms.txt lacks the published page:\n%s", idx)
	}
	if !strings.Contains(full, "# Pricing") || !strings.Contains(full, "Fixed prices for small sites.") {
		t.Errorf("llms-full.txt lacks the published page")
	}

	// A person's job title: the file changes and the card gets a change line.
	attrs := jsontext.Value(`{"public":true,"job_title":"Design director","bio":"<p>Elena leads design.</p>"}`)
	if _, err := svc.UpdateEntity(e.ctx, e.ids["elena"], nil, &entity.UpdateEntityInput{Attributes: &attrs}); err != nil {
		t.Fatal(err)
	}
	e.srv.hooks.Wait()
	after := e.ok(t, "/llms.txt")
	if !strings.Contains(after, "Elena Voss](https://acme.example/author/elena-voss): Design director") {
		t.Errorf("llms.txt still has the old job title:\n%s", after)
	}
	st, err := e.srv.aiFilesState.Get(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, c := range st.Changes {
		texts = append(texts, c.Text)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "Elena Voss: job title changed") {
		t.Errorf("recent changes = %q", texts)
	}
	if st.LLMsChangedAt == nil || st.FullChangedAt == nil || st.LLMsBytes == 0 || st.FullBytes == 0 {
		t.Errorf("state not recorded: %+v", st)
	}
}

func TestLLMsFullTruncation(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{aiFiles: `{"full_cap_kb":64}`, bulk: true})
	full := e.ok(t, "/llms-full.txt")
	if len(full) > 64<<10 {
		t.Errorf("file is %d bytes, over the 64 KB cap", len(full))
	}
	if !strings.Contains(full, "This file was truncated at 64 KB.") {
		t.Errorf("no truncation line; tail: %q", full[max(0, len(full)-300):])
	}
}

func TestLLMsAccessGate(t *testing.T) {
	for _, mode := range []struct {
		name string
		want int
	}{{"coming-soon", 404}, {"maintenance", 503}} {
		t.Run(mode.name, func(t *testing.T) {
			e := newLLMsEnv(t, llmsOpts{access: mode.name})
			for _, p := range []string{"/llms.txt", "/llms-full.txt", "/api/v1/public/llms.txt", "/api/v1/public/llms-full.txt"} {
				code, body, h := e.get(p)
				if code != mode.want {
					t.Errorf("%s = %d, want %d", p, code, mode.want)
				}
				if !strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
					t.Errorf("%s content type %q", p, h.Get("Content-Type"))
				}
				for _, s := range []string{"Acme", "Elena", "Services"} {
					if strings.Contains(body, s) {
						t.Errorf("%s leaks %q: %q", p, s, body)
					}
				}
			}
		})
	}
}

func TestLLMsSwitchedOff(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{aiFiles: `{"llms_off":true,"full_off":true}`})
	for _, p := range []string{"/llms.txt", "/llms-full.txt", "/api/v1/public/llms.txt", "/api/v1/public/llms-full.txt"} {
		if code, body, _ := e.get(p); code != 404 || strings.Contains(body, "Acme") {
			t.Errorf("%s = %d %q, want 404", p, code, body)
		}
	}
}

// A build made with an unreadable site config is served, but neither cached nor
// recorded: its fallback name and empty summary would fill the change list.
func TestLLMsDegradedNotCachedOrRecorded(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{})
	e.srv.cfg.PublicSite.BaseURL = "https://acme.example" // keep the base independent of the site config
	dead, err := database.New(e.ctx, e.srv.cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	e.srv.siteConfig = siteconfig.NewStore(dead, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/llms.txt", "/llms-full.txt"} {
		rec := httptest.NewRecorder() // the handler direct: the middleware also needs the site config
		e.srv.serveLLMs(rec, httptest.NewRequest("GET", path, nil), path == "/llms-full.txt")
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		if body, _ := e.srv.infra.Cache.Get(e.ctx, cache.RenderedPageKey("llms:https://acme.example", path)); body != nil {
			t.Errorf("%s: a degraded build was cached", path)
		}
	}
	st, err := e.srv.aiFilesState.Get(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.LLMsChangedAt != nil || st.FullChangedAt != nil || len(st.Changes) != 0 {
		t.Errorf("a degraded build was recorded: %+v", st)
	}
}

// With no site address configured the base comes from the Host header; such a
// build is served but must not move the stored state.
func TestLLMsRequestDerivedBaseNotRecorded(t *testing.T) {
	e := newLLMsEnv(t, llmsOpts{})
	if _, err := e.pool.Exec(e.ctx, `UPDATE hatamoto_site_config SET public_base_url = ''`); err != nil {
		t.Fatal(err)
	}
	e.srv.siteConfig = siteconfig.NewStore(e.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/llms.txt", "/llms-full.txt"} {
		rec := httptest.NewRecorder()
		e.srv.serveLLMs(rec, httptest.NewRequest("GET", "http://other.example"+path, nil), path == "/llms-full.txt")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "http://other.example") {
			t.Fatalf("GET %s = %d, body %.200s", path, rec.Code, rec.Body.String())
		}
	}
	st, err := e.srv.aiFilesState.Get(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.LLMsChangedAt != nil || st.FullChangedAt != nil || len(st.Snapshot) != 0 {
		t.Errorf("a Host-derived build was recorded: %+v", st)
	}
}
