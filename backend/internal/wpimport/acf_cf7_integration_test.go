//go:build integration

package wpimport

import (
	"bytes"
	"context"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

// acfEnv is one import of a fixture WXR into a real database. The WXR points
// at a local server that serves the media, so every environment has a host of
// its own and the subtests share no state.
type acfEnv struct {
	t     *testing.T
	ctx   context.Context
	d     *Deps
	db    *database.DB
	srv   *httptest.Server
	host  string
	dump  *Dump
	store *Store
}

// newACFEnv parses fixture for a fresh local host. dropSchemas names the
// custom types the run creates or extends: they are deleted again at the end.
func newACFEnv(t *testing.T, fixture string, dropSchemas ...string) *acfEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir(), Secret: "integration-test-secret"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(req.URL.Path))
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(1, 1, color.RGBA{R: uint8(h.Sum32()), G: uint8(h.Sum32() >> 8), A: 255})
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, nil)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)

	// The items need the site row a booted site has.
	if tag, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('wpacf test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() == 1 {
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_site_config WHERE company_name = 'wpacf test'`)
		})
	}

	e := &acfEnv{t: t, ctx: ctx, d: d, db: db, srv: srv, host: strings.TrimPrefix(srv.URL, "http://")}
	e.dump, err = ParseWXR(strings.NewReader(strings.ReplaceAll(fixture, "https://old.example.test", srv.URL)), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.store = NewStore(db.Pool(), srv.URL)

	cleanup := func() {
		pool := db.Pool()
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE metadata->>'import_key' LIKE $1`, "wp:"+e.host+":%")
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_forms WHERE id IN (SELECT target_id FROM hatamoto_import_map WHERE source = $1 AND kind = 'form')`, e.store.Ref())
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_import_runs WHERE source = $1`, e.store.Ref())
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_import_map WHERE source = $1`, e.store.Ref())
		for _, slug := range dropSchemas {
			_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug = $1 AND NOT is_builtin`, slug)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return e
}

// run imports the dump once: schemas, media, forms, items and the second pass.
func (e *acfEnv) run() *Runner {
	e.t.Helper()
	r, err := NewRunner(e.d, e.store, e.dump, Options{AllowPrivate: true})
	if err != nil {
		e.t.Fatal(err)
	}
	if r.run, err = e.store.StartRun(e.ctx, RunRunning, nil); err != nil {
		e.t.Fatal(err)
	}
	r.res.RunID = r.run.ID
	if r.res.RunNumber, err = e.store.RunNumber(e.ctx, r.run.ID); err != nil {
		e.t.Fatal(err)
	}
	r.state = &SiteState{Mode: ModeExisting}
	steps := []func(context.Context) error{
		r.schemasStep,
		func(ctx context.Context) error { r.importMedia(ctx); return nil },
		r.contentStep, r.linksStep,
	}
	for _, step := range steps {
		if err := step(e.ctx); err != nil {
			e.t.Fatal(err)
		}
	}
	if r.res.Failed != 0 || len(r.res.Errors) > 0 {
		e.t.Fatalf("failed=%d errors=%+v", r.res.Failed, r.res.Errors)
	}
	r.res.Report = Scan(e.dump, e.store.Ref())
	if err := e.store.FinishRun(e.ctx, r.run.ID, RunCompleted, r.res, nil); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *acfEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Pool().QueryRow(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *acfEnv) text(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.db.Pool().QueryRow(e.ctx, q, args...).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// counts are the sizes the second run must leave as they are.
type acfCounts struct{ Entities, Map, Forms int }

func (e *acfEnv) counts() acfCounts {
	return acfCounts{
		Entities: e.count(`SELECT count(*) FROM hatamoto_entities WHERE metadata->>'import_key' LIKE $1`, "wp:"+e.host+":%"),
		Map:      e.count(`SELECT count(*) FROM hatamoto_import_map WHERE source = $1`, e.store.Ref()),
		Forms:    e.count(`SELECT count(*) FROM hatamoto_forms`),
	}
}

// schemaJSON is the stored JSON of a schema, "" when it does not exist.
func (e *acfEnv) schemaJSON(slug string) string {
	e.t.Helper()
	var s string
	err := e.db.Pool().QueryRow(e.ctx, `SELECT schema::text FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(&s)
	if err != nil {
		return ""
	}
	return s
}

// props are the properties of a stored schema.
func (e *acfEnv) props(slug string) map[string]map[string]any {
	e.t.Helper()
	var sc struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := jsonx.Unmarshal([]byte(e.schemaJSON(slug)), &sc); err != nil {
		e.t.Fatalf("schema %s: %v", slug, err)
	}
	return sc.Properties
}

// attrs are the attributes of the imported item with the given source id.
func (e *acfEnv) attrs(wpID string) map[string]any {
	e.t.Helper()
	raw := e.text(`SELECT attributes::text FROM hatamoto_entities WHERE metadata->>'import_key' = $1`, KeyPrefix(e.srv.URL)+wpID)
	var m map[string]any
	if err := jsonx.Unmarshal([]byte(raw), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *acfEnv) entityID(wpID string) string {
	e.t.Helper()
	return e.text(`SELECT id::text FROM hatamoto_entities WHERE metadata->>'import_key' = $1`, KeyPrefix(e.srv.URL)+wpID)
}

func hasWarning(res *Result, sub string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func reportText(res *Result) string {
	var b strings.Builder
	WriteReportText(&b, res)
	return b.String()
}

// A WXR without ACF or Contact Form 7 imports as before.
func TestImportPlainBlogHasNoACFOrForms_Integration(t *testing.T) {
	e := newACFEnv(t, plainBlogWXR)
	r := e.run()
	if r.res.ACF != nil || r.res.Forms != nil {
		t.Errorf("plain blog has ACF %+v or forms %+v", r.res.ACF, r.res.Forms)
	}
	if r.res.Created != 3 {
		t.Errorf("created = %d, want 3", r.res.Created)
	}
	if got := reportText(r.res); got != "" {
		t.Errorf("custom fields report = %q, want none", got)
	}
}

// The directory fixture: every field type of the listing lands as an
// attribute, a clash is skipped, and the second run changes nothing.
func TestImportACFDirectory_Integration(t *testing.T) {
	e := newACFEnv(t, acfDirectoryWXR, "listing")
	// The site already has a listing type whose price is a boolean.
	if _, err := e.d.Entities.CreateSchema(e.ctx, &entity.CreateSchemaInput{Name: "Listing", Slug: "listing",
		Schema: []byte(`{"type":"object","properties":{"price":{"type":"boolean"}}}`)}); err != nil {
		t.Fatal(err)
	}
	r1 := e.run()
	acf := r1.res.ACF
	if acf == nil || acf.FieldsAdded == 0 {
		t.Fatalf("run 1 ACF = %+v", acf)
	}
	props := e.props("listing")
	if props["price"]["type"] != "boolean" {
		t.Errorf("price was retyped: %v", props["price"])
	}
	for _, name := range []string{"phone", "photo", "website", "opening_date", "size", "staff", "related", "gallery", "map"} {
		if _, ok := props[name]; !ok {
			t.Errorf("listing has no property %q: %v", name, props)
		}
	}
	if !strings.Contains(reportText(r1.res), "a property price of another type exists; field skipped") {
		t.Errorf("no line for the price clash:\n%s", reportText(r1.res))
	}
	if !hasWarning(r1.res, "stored as text") {
		t.Errorf("no \"stored as text\" warning: %v", r1.res.Warnings)
	}

	a := e.attrs("2")
	if photo, _ := a["photo"].(string); !strings.Contains(photo, "/media/") {
		t.Errorf("photo = %v, want a /media/ URL", a["photo"])
	}
	if staff, _ := a["staff"].([]any); len(staff) != 3 {
		t.Errorf("staff = %v, want 3 rows", a["staff"])
	} else if _, ok := staff[0].(map[string]any); !ok {
		t.Errorf("staff row = %v, want an object", staff[0])
	}
	if rel, _ := a["related"].([]any); len(rel) != 1 || rel[0] != e.entityID("1") {
		t.Errorf("related = %v, want [%s]", a["related"], e.entityID("1"))
	}
	if g, _ := a["gallery"].([]any); len(g) != 3 {
		t.Errorf("gallery = %v, want 3 URLs", a["gallery"])
	} else if s, _ := g[0].(string); !strings.Contains(s, "/media/") {
		t.Errorf("gallery[0] = %v, want a /media/ URL", g[0])
	}
	if _, ok := a["opening_date_raw"]; !ok {
		t.Errorf("the bad date left no opening_date_raw: %v", a)
	}
	if _, ok := a["map"].(string); !ok {
		t.Errorf("map = %v (%T), want a string", a["map"], a["map"])
	}

	before, schemaBefore := e.counts(), e.schemaJSON("listing")
	r2 := e.run()
	if r2.res.ACF.FieldsAdded != 0 || r2.res.ACF.RelationsChanged != 0 || r2.res.Created != 0 {
		t.Errorf("run 2 added %d fields, changed %d relations, created %d", r2.res.ACF.FieldsAdded, r2.res.ACF.RelationsChanged, r2.res.Created)
	}
	if after := e.counts(); after != before {
		t.Errorf("counts %+v -> %+v", before, after)
	}
	if e.schemaJSON("listing") != schemaBefore {
		t.Error("the listing schema changed on run 2")
	}
	if rel, _ := e.attrs("2")["related"].([]any); len(rel) != 1 {
		t.Errorf("related after run 2 = %v", rel)
	}
	if len(r2.res.OverwrittenEdits) != 0 {
		t.Errorf("run 2 overwrote %v", r2.res.OverwrittenEdits)
	}
	if got := reportText(r2.res); !strings.Contains(got, "nothing new, 0 changed") {
		t.Errorf("run 2 report:\n%s", got)
	}
}

// Flexible content: built-in schemas are never written, a custom type gains
// an array of layouts, nested flexible content is kept as text.
func TestImportACFFlexible_Integration(t *testing.T) {
	e := newACFEnv(t, acfFlexibleWXR, "landing")
	pageBefore := e.schemaJSON("page")
	r1 := e.run()
	if e.schemaJSON("page") != pageBefore {
		t.Error("the built-in page schema was written")
	}
	a := e.attrs("10")
	sections, _ := a["sections"].([]any)
	if len(sections) != 3 {
		t.Fatalf("sections = %v, want 3 rows", a["sections"])
	}
	for i, want := range []string{"hero", "text_block", "columns"} {
		if row, _ := sections[i].(map[string]any); row["layout"] != want {
			t.Errorf("row %d = %v, want layout %s", i, sections[i], want)
		}
	}
	if cols, _ := sections[2].(map[string]any); cols["cols"] == nil {
		t.Errorf("columns row = %v", sections[2])
	} else if _, ok := cols["cols"].(string); !ok {
		t.Errorf("nested cols = %v (%T), want a string", cols["cols"], cols["cols"])
	}
	s := e.props("landing")["sections"]
	if s["type"] != "array" {
		t.Fatalf("landing sections = %v", s)
	}
	items, _ := s["items"].(map[string]any)
	props, _ := items["properties"].(map[string]any)
	layout, _ := props["layout"].(map[string]any)
	if items["type"] != "object" || layout["enum"] == nil {
		t.Errorf("sections items = %v, want an object with a layout enum", items)
	}
	text := reportText(r1.res)
	if strings.Count(text, "Partly") < 2 {
		t.Errorf("want a built-in line and a nested line, report:\n%s", text)
	}
	if !strings.Contains(text, "nested content kept as text") {
		t.Errorf("no nested line:\n%s", text)
	}

	before := e.counts()
	r2 := e.run()
	if r2.res.ACF.FieldsAdded != 0 || r2.res.ACF.RelationsChanged != 0 {
		t.Errorf("run 2 ACF = %+v", r2.res.ACF)
	}
	if after := e.counts(); after != before {
		t.Errorf("counts %+v -> %+v", before, after)
	}
	if e.schemaJSON("page") != pageBefore {
		t.Error("the built-in page schema was written on run 2")
	}
}

// importCF7 runs the forms fixture twice. With clash the owner already has a
// form with the slug "contact", which the import must leave alone.
func importCF7(t *testing.T, clash bool) {
	e := newACFEnv(t, cf7FormWXR)
	wantSlug := "contact"
	if clash {
		wantSlug = "contact-2"
	}
	drop := func() {
		_, _ = e.db.Pool().Exec(e.ctx, `DELETE FROM hatamoto_forms WHERE slug IN ('contact', 'contact-2', 'quote-request')`)
	}
	drop()
	t.Cleanup(drop)
	var mine *form.Form
	if clash {
		var err error
		mine, err = e.d.Forms.CreateForm(e.ctx, &form.CreateFormInput{Name: "Mine", Slug: "contact", Status: form.StatusActive,
			Fields: []form.FormField{{ID: "f1", Type: "text", Label: "Mine", Name: "mine"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	baseForms := e.count(`SELECT count(*) FROM hatamoto_forms`)

	r1 := e.run()
	fr := r1.res.Forms
	if fr == nil || fr.Found != 2 || fr.Created != 2 || !fr.MailNotImported {
		t.Fatalf("forms result = %+v", fr)
	}
	if got := e.count(`SELECT count(*) FROM hatamoto_forms`); got != baseForms+2 {
		t.Errorf("forms = %d, want %d", got, baseForms+2)
	}
	for _, f := range fr.Forms {
		if strings.Contains(f.Title, "Quote") && f.Status != "partly" {
			t.Errorf("quote form status = %q, want partly (quiz dropped)", f.Status)
		}
	}
	rows, err := e.db.Pool().Query(e.ctx, `SELECT target_id FROM hatamoto_import_map WHERE source = $1 AND kind = 'form'`, e.store.Ref())
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	slugs := map[string]*form.Form{}
	for _, id := range ids {
		f, err := e.d.Forms.GetForm(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		slugs[f.Slug] = f
		if f.Settings.NotifyEmail != nil {
			t.Errorf("form %s has a notify address", f.Slug)
		}
	}
	if slugs[wantSlug] == nil || slugs["quote-request"] == nil || len(slugs) != 2 {
		t.Fatalf("imported slugs = %v, want %s and quote-request", slugs, wantSlug)
	}
	if n := len(slugs[wantSlug].Fields); n != 5 {
		t.Errorf("contact fields = %d, want 5", n)
	}
	for _, f := range slugs["quote-request"].Fields {
		if f.Type == "quiz" {
			t.Errorf("the quiz field was kept: %+v", f)
		}
	}
	if mine != nil {
		if kept, err := e.d.Forms.GetForm(e.ctx, mine.ID); err != nil || kept.Name != "Mine" || len(kept.Fields) != 1 {
			t.Errorf("the owner's form changed: %+v (%v)", kept, err)
		}
	}

	// The sanitiser stores the quotes of the shortcode as &#34;, which the
	// shortcode expander reads back.
	body := strings.ReplaceAll(e.text(`SELECT coalesce(attributes->>'content', '') || coalesce(attributes->>'body', '') FROM hatamoto_entities WHERE metadata->>'import_key' = $1`, KeyPrefix(e.srv.URL)+"310"), "&#34;", `"`)
	for _, s := range []string{`[form slug="` + wantSlug + `"]`, `[form slug="quote-request"]`} {
		if strings.Count(body, s) != 1 {
			t.Errorf("page body lacks %s once: %q", s, body)
		}
	}
	if strings.Contains(body, "contact-form-7") {
		t.Errorf("a Contact Form 7 shortcode is left: %q", body)
	}

	before := e.counts()
	r2 := e.run()
	if f := r2.res.Forms; f == nil || f.Created != 0 || f.AlreadyImported != 2 {
		t.Errorf("run 2 forms = %+v", f)
	}
	if after := e.counts(); after != before {
		t.Errorf("counts %+v -> %+v", before, after)
	}
	if got := reportText(r2.res); !strings.Contains(got, "nothing new, 0 changed") {
		t.Errorf("run 2 report:\n%s", got)
	}
}

// Contact Form 7 forms are created, their shortcodes point at them, and a
// second run changes nothing.
func TestImportCF7Forms_Integration(t *testing.T) { importCF7(t, false) }

// A form slug that is taken gets a number and the owner's form is untouched.
func TestImportCF7SlugClash_Integration(t *testing.T) { importCF7(t, true) }

// acfBuiltinWXR has one field group on the built-in post and page types whose
// field names are properties of those types, plus an item of each.
func acfBuiltinWXR() string {
	field := func(id, key, name, typ string) string {
		return ` <item>
  <title>` + name + `</title><link>https://old.example.test/` + key + `/</link><dc:creator><![CDATA[editor]]></dc:creator>
  <content:encoded><![CDATA[a:1:{s:4:"type";s:` + typ + `;}]]></content:encoded><excerpt:encoded><![CDATA[` + name + `]]></excerpt:encoded>
  <wp:post_id>` + id + `</wp:post_id><wp:post_name><![CDATA[` + key + `]]></wp:post_name><wp:status><![CDATA[publish]]></wp:status><wp:post_parent>300</wp:post_parent><wp:menu_order>0</wp:menu_order><wp:post_type><![CDATA[acf-field]]></wp:post_type>
 </item>
`
	}
	item := func(id, typ, name, key, val string) string {
		return ` <item>
  <title>Sample ` + typ + `</title><link>https://old.example.test/sample-` + typ + `/</link><dc:creator><![CDATA[editor]]></dc:creator>
  <content:encoded><![CDATA[<p>Sample</p>]]></content:encoded><excerpt:encoded><![CDATA[]]></excerpt:encoded>
  <wp:post_id>` + id + `</wp:post_id><wp:post_name><![CDATA[sample-` + typ + `]]></wp:post_name><wp:status><![CDATA[publish]]></wp:status><wp:post_parent>0</wp:post_parent><wp:menu_order>0</wp:menu_order><wp:post_type><![CDATA[` + typ + `]]></wp:post_type>
 <wp:postmeta><wp:meta_key><![CDATA[` + name + `]]></wp:meta_key><wp:meta_value><![CDATA[` + val + `]]></wp:meta_value></wp:postmeta>
 <wp:postmeta><wp:meta_key><![CDATA[_` + name + `]]></wp:meta_key><wp:meta_value><![CDATA[` + key + `]]></wp:meta_value></wp:postmeta>
 </item>
`
	}
	loc := func(typ string) string {
		return `i:` + map[string]string{"post": "0", "page": "1"}[typ] + `;a:1:{i:0;a:3:{s:5:"param";s:9:"post_type";s:8:"operator";s:2:"==";s:5:"value";s:` + map[string]string{"post": "4", "page": "4"}[typ] + `:"` + typ + `";}}`
	}
	return `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/"
 xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
 <title>Builtin Site</title><link>https://old.example.test</link><description>Fixture</description><language>en-US</language>
 <wp:base_site_url>https://old.example.test</wp:base_site_url>
 <wp:author><wp:author_login><![CDATA[editor]]></wp:author_login><wp:author_display_name><![CDATA[Sample Editor]]></wp:author_display_name></wp:author>
 <item>
  <title>Builtin names</title><link>https://old.example.test/group_00000000000d1/</link><dc:creator><![CDATA[editor]]></dc:creator>
  <content:encoded><![CDATA[a:1:{s:8:"location";a:2:{` + loc("post") + loc("page") + `}}]]></content:encoded><excerpt:encoded><![CDATA[]]></excerpt:encoded>
  <wp:post_id>300</wp:post_id><wp:post_name><![CDATA[group_00000000000d1]]></wp:post_name><wp:status><![CDATA[publish]]></wp:status><wp:post_parent>0</wp:post_parent><wp:menu_order>0</wp:menu_order><wp:post_type><![CDATA[acf-field-group]]></wp:post_type>
 </item>
` + field("301", "field_00000000000e1", "description", "4:\"text\"") +
		field("302", "field_00000000000e2", "series_part", "6:\"number\"") +
		item("10", "page", "description", "field_00000000000e1", "About us") +
		item("11", "post", "series_part", "field_00000000000e2", "2.5") + `</channel>
</rss>
`
}

// ACF values never go into a property the built-in types declare: a field
// named like one is stored under an acf_ name, and the items import.
func TestImportACFBuiltinNamesGetAPrefix_Integration(t *testing.T) {
	e := newACFEnv(t, acfBuiltinWXR())
	e.run()
	page, post := e.attrs("10"), e.attrs("11")
	if _, ok := page["description"]; ok || page["acf_description"] != "About us" {
		t.Errorf("page attributes = %v, want acf_description and no description", page)
	}
	if _, ok := post["series_part"]; ok || post["acf_series_part"] != 2.5 {
		t.Errorf("post attributes = %v, want acf_series_part 2.5 and no series_part", post)
	}
	e.run()
}

// A property the owner retyped after the import is left alone on the next
// run; the item still imports.
func TestImportACFRetypedPropertyKeepsItemsImporting_Integration(t *testing.T) {
	e := newACFEnv(t, acfDirectoryWXR, "listing")
	if _, err := e.d.Entities.CreateSchema(e.ctx, &entity.CreateSchemaInput{Name: "Listing", Slug: "listing",
		Schema: []byte(`{"type":"object","properties":{"price":{"type":"boolean"}}}`)}); err != nil {
		t.Fatal(err)
	}
	e.run()
	if _, err := e.db.Pool().Exec(e.ctx, `UPDATE hatamoto_entity_schemas SET schema = jsonb_set(schema, '{properties,size}', '{"type":"boolean"}') WHERE slug = 'listing'`); err != nil {
		t.Fatal(err)
	}
	r2 := e.run() // fails the run when an item cannot be saved
	if got := reportText(r2.res); !strings.Contains(got, "was changed to another type") {
		t.Errorf("run 2 report has no line for the retyped property:\n%s", got)
	}
}
