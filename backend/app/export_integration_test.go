//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/portability"
	"hatamoto.dev/core/internal/server"
)

// scratchDB creates an empty database next to DATABASE_URL's and returns its
// URL and a pool on it.
func scratchDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_exp_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String(), pool
}

// renderKeyURLs boots the whole server on dsn and returns what key URLs serve.
func renderKeyURLs(t *testing.T, dsn, mediaDir string, paths []string) map[string]string {
	t.Helper()
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "export-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Export IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Export IT")
	t.Setenv("PUBLIC_BASE_URL", "https://export-it.example.test")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", mediaDir)
	t.Setenv("HATAMOTO_WORKER", "off")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := server.New(cfg, db, quietLogger()).Handler()
	out := map[string]string{}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		out[p] = http.StatusText(rec.Code) + "\n" + rec.Body.String()
	}
	return out
}

// Neutral business details that use every field added for the locale site graph.
const businessProfileSeed = `{
  "name": "Example Co", "tax_id": "TAX-0001", "vat_id": "VAT-0001",
  "alternate_name": "Example", "description": "Makes examples.", "slogan": "Examples everywhere",
  "founding_date": "2009-05", "employees_min": 10, "employees_max": 50,
  "logo_object": true, "org_address": "locale_or_site",
  "address": {"street": "1 Example Street", "city": "Example City", "country": "XX", "lat": "-33.5", "lng": "151.25"},
  "phone": "+00 555 0100",
  "contact_points": [{"type": "sales", "name": "Sales", "phone": "+00 555 0101", "area_served": "XX", "available_language": "English"}],
  "opening_hours": [{"days": ["Monday", "Tuesday"], "opens": "09:00", "closes": "17:00"}],
  "special_hours": [{"from": "2030-12-24", "to": "2030-12-26", "closed": true}, {"from": "2030-12-31", "opens": "10:00", "closes": "12:00"}],
  "rating": {"value": "8.5", "best": "10", "worst": "1", "count": "120", "count_type": "reviews"}
}`

const localeProfilesSeed = `{
  "en-XX": {
    "name": "Example Co XX", "tax_id": "TAX-XX", "vat_id": "VAT-XX", "country_name": "Exampleland",
    "description": "The XX office.", "image": "/media/tenant/pic.png",
    "address": {"street": "2 Example Street", "city": "Example Town", "country": "XX"},
    "phone": "+00 555 0200"
  },
  "en-YY": {"no_office": true, "country_name": "Otherland", "phone": "+00 555 0300"}
}`

func TestExportImportRoundTrip_Integration(t *testing.T) {
	ctx := context.Background()
	srcDSN, src := scratchDB(t)
	dstDSN, dst := scratchDB(t)
	for _, p := range []*pgxpool.Pool{src, dst} {
		if err := migrateUp(ctx, p, quietLogger()); err != nil {
			t.Fatal(err)
		}
	}
	if err := firstBoot(ctx, src, "Export IT", quietLogger()); err != nil {
		t.Fatal(err)
	}
	srcMedia, dstMedia := t.TempDir(), t.TempDir()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := src.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// ---- seed a small site
	owner, parentID, childID, postID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role,name) VALUES ($1,'owner@example.test','$2a$10$secret-hash-must-not-travel','owner','Owner')`, owner)
	exec(`UPDATE hatamoto_site_config SET tagline='Round trip', language='en'`)
	// Business details with every organisation, contact point, rating, hours and card field.
	exec(`UPDATE hatamoto_site_config SET business_profile=$1::jsonb, locale_profiles=$2::jsonb`, businessProfileSeed, localeProfilesSeed)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,created_by,created_at,updated_at,attributes)
		SELECT $1,id,'Services','services','published','2026-03-01T10:00:00Z',$2,'2026-02-01T09:00:00Z','2026-03-02T11:12:13.123456Z',
		'{"page_sections":[{"id":"h","type":"hero","sort_order":1,"content":{"headline":"Our services"}}]}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug='page'`, parentID, owner)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,parent_id,created_at,updated_at,attributes)
		SELECT $1,id,'Local SEO','local-seo','published','2026-03-03T10:00:00Z',$2,'2026-02-02T09:00:00Z','2026-03-04T11:12:13Z',
		'{"meta_title":"Local SEO page"}'::jsonb FROM hatamoto_entity_schemas WHERE slug='page'`, childID, parentID)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,created_at,updated_at,attributes)
		SELECT $1,id,'Hello Post','hello-post','published','2026-04-01T10:00:00Z','2026-04-01T09:00:00Z','2026-04-01T10:00:00Z',
		'{"excerpt":"An excerpt","content":"<p>Post body</p>"}'::jsonb FROM hatamoto_entity_schemas WHERE slug='post'`, postID)
	exec(`INSERT INTO hatamoto_entity_versions (entity_id,version,attributes,changed_by,change_summary,created_at)
		VALUES ($1,1,'{"content":"<p>v1</p>"}',$2,'first','2026-04-01T09:30:00Z')`, postID, owner)
	termID := uuid.New()
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,'tag','news','News')`, termID)
	exec(`INSERT INTO hatamoto_entity_terms (entity_id,term_id) VALUES ($1,$2)`, postID, termID)
	menuID, topItem := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_menus (id,name,slug,location) VALUES ($1,'Main','main','header')`, menuID)
	exec(`INSERT INTO hatamoto_menu_items (id,menu_id,label,link_type,entity_id,position) VALUES ($1,$2,'Services','entity',$3,0)`, topItem, menuID, parentID)
	exec(`INSERT INTO hatamoto_menu_items (menu_id,parent_id,label,link_type,entity_id,position,updated_at) VALUES ($1,$2,'Local SEO','entity',$3,0,'2026-01-01T00:00:00Z')`, menuID, topItem, childID)
	exec(`INSERT INTO hatamoto_redirects (source_path,target_url,status_code) VALUES ('/old-services','/services',301)`)
	formID := uuid.New()
	exec(`INSERT INTO hatamoto_forms (id,name,slug,fields) VALUES ($1,'Contact','contact','[{"name":"email","label":"Email","type":"email","required":true,"position":1}]')`, formID)
	exec(`INSERT INTO hatamoto_form_submissions (form_id,data) VALUES ($1,'{"email":"visitor@example.test"}')`, formID)
	mediaBytes := bytes.Repeat([]byte("\x89PNG\r\n\x1a\nfake-bytes-"), 1000)
	variantBytes := []byte("\x89PNG\r\n\x1a\nvariant bytes")
	if err := os.MkdirAll(filepath.Join(srcMedia, "tenant"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"tenant/pic.png": mediaBytes, "tenant/pic_thumbnail.png": variantBytes} {
		if err := os.WriteFile(filepath.Join(srcMedia, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,variants)
		VALUES ('pic.png','pic.png','image/png',$1,'tenant/pic.png','/media/tenant/pic.png',
		'[{"size":"thumbnail","path":"tenant/pic_thumbnail.png","url":"/media/tenant/pic_thumbnail.png","width":1,"height":1}]')`, len(mediaBytes))

	// A WebP upload: older versions wrote its resized copies as JPEG bytes under
	// a .webp name. The export must still restore.
	webpBytes := []byte("RIFF\x00\x00\x00\x00WEBPVP8 webp body")
	jpegVariant := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00 jpeg bytes named webp")
	for name, b := range map[string][]byte{"tenant/w.webp": webpBytes, "tenant/w_thumbnail.webp": jpegVariant} {
		if err := os.WriteFile(filepath.Join(srcMedia, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,variants)
		VALUES ('w.webp','w.webp','image/webp',$1,'tenant/w.webp','/media/tenant/w.webp',
		'[{"size":"thumbnail","path":"tenant/w_thumbnail.webp","url":"/media/tenant/w_thumbnail.webp","width":1,"height":1}]')`, len(webpBytes))

	// Authors (with an avatar), the author link, and a term tree with a custom kind.
	authorID, parentTerm, childTerm := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_authors (id,slug,name,bio,avatar_media_id) SELECT $1,'jane-doe','Jane Doe','Writes things',id FROM hatamoto_media LIMIT 1`, authorID)
	exec(`UPDATE hatamoto_entities SET author_id=$1 WHERE id=$2`, authorID, postID)
	// A site that has people: Jane's person, credited on the post and linked to the only login.
	sdb := stdlib.OpenDBFromPool(src)
	defer sdb.Close()
	if err := person.Backfill(ctx, sdb); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name,description) VALUES ($1,'region','north','Northern','Down under')`, parentTerm)
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name,parent_id) VALUES ($1,'region','north-east','North East',$2)`, childTerm, parentTerm)

	// A {path} schema whose siblings repeat a slug under different parents.
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ('44444444-4444-4444-4444-444444444444','Region','region','{"type":"object"}','{"url_pattern":"/{path}"}')`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug) VALUES ('44444444-0000-0000-0000-000000000001','44444444-4444-4444-4444-444444444444','North','north'),('44444444-0000-0000-0000-000000000002','44444444-4444-4444-4444-444444444444','South','south')`)
	exec(`INSERT INTO hatamoto_entities (schema_id,name,slug,parent_id) VALUES ('44444444-4444-4444-4444-444444444444','North seo','seo','44444444-0000-0000-0000-000000000001'),('44444444-4444-4444-4444-444444444444','South seo','seo','44444444-0000-0000-0000-000000000002')`)

	// ---- export, then import into the empty second database
	archive := filepath.Join(t.TempDir(), "site.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	man, err := portability.Export(ctx, src, f, portability.ExportOptions{MediaDir: srcMedia})
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if man.MediaFiles != 4 || man.Rows["hatamoto_entities"] != 9 || man.Rows["hatamoto_user_people"] != 1 || man.Rows["hatamoto_authors"] != 1 {
		t.Fatalf("manifest = %+v", man)
	}
	raw, _ := os.ReadFile(archive)
	if gz, _ := gzip.NewReader(bytes.NewReader(raw)); gz != nil {
		b, _ := io.ReadAll(gz)
		if strings.Contains(string(b), "secret-hash-must-not-travel") {
			t.Fatal("password hash leaked into the archive")
		}
	}

	open := func() *os.File {
		t.Helper()
		f, err := os.Open(archive)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	// A database with content refuses the import.
	if _, err := portability.Import(ctx, src, open(), portability.ImportOptions{MediaDir: t.TempDir()}); !errors.Is(err, portability.ErrNotEmpty) {
		t.Fatalf("import into a populated database: %v", err)
	}
	// A truncated archive imports nothing.
	if _, err := portability.Import(ctx, dst, bytes.NewReader(raw[:len(raw)/2]), portability.ImportOptions{MediaDir: dstMedia}); err == nil {
		t.Fatal("truncated archive imported")
	}
	var n int
	if err := dst.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed import left %d entities (%v)", n, err)
	}

	res, err := portability.Import(ctx, dst, open(), portability.ImportOptions{MediaDir: dstMedia})
	if err != nil {
		t.Fatal(err)
	}
	if res.MediaFiles != 4 || len(res.Warnings) != 0 {
		t.Errorf("media files = %d", res.MediaFiles)
	}

	// ---- compare
	for _, tbl := range []string{"hatamoto_entity_schemas", "hatamoto_users", "hatamoto_site_config", "hatamoto_entities", "hatamoto_entity_versions",
		"hatamoto_terms", "hatamoto_entity_terms", "hatamoto_menus", "hatamoto_menu_items", "hatamoto_redirects", "hatamoto_forms",
		"hatamoto_form_submissions", "hatamoto_media", "hatamoto_section_templates", "hatamoto_authors", "hatamoto_user_people"} {
		var a, b int
		_ = src.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&a)
		_ = dst.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&b)
		if a != b {
			t.Errorf("%s: %d rows in source, %d imported", tbl, a, b)
		}
	}
	// Ids, timestamps (to the microsecond) and parent links survive.
	dump := func(p *pgxpool.Pool, q string) string {
		var s string
		if err := p.QueryRow(ctx, q).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, q := range []string{
		`SELECT string_agg(id||'|'||created_at||'|'||updated_at||'|'||coalesce(parent_id::text,'-')||'|'||coalesce(published_at::text,'-')||'|'||attributes::text, ';' ORDER BY id) FROM (SELECT id::text,created_at::text,updated_at::text,parent_id,published_at,attributes FROM hatamoto_entities) e`,
		`SELECT string_agg(id::text||'|'||coalesce(parent_id::text,'-')||'|'||updated_at::text, ';' ORDER BY id) FROM hatamoto_menu_items`,
		`SELECT string_agg(id::text||'|'||coalesce(author_id::text,'-')||'|'||slug_nested, ';' ORDER BY id) FROM hatamoto_entities`,
		`SELECT string_agg(row_to_json(a)::text, ';' ORDER BY id) FROM hatamoto_authors a`,
		`SELECT string_agg(id::text||'|'||kind||'|'||slug||'|'||description||'|'||coalesce(parent_id::text,'-'), ';' ORDER BY id) FROM hatamoto_terms`,
		`SELECT string_agg(entity_id::text||'|'||version||'|'||created_at::text, ';') FROM hatamoto_entity_versions`,
		`SELECT row_to_json(c)::text FROM hatamoto_site_config c`,
	} {
		if a, b := dump(src, q), dump(dst, q); a != b || a == "" {
			t.Errorf("mismatch for %.60s…\n src: %s\n dst: %s", q, a, b)
		}
	}
	for _, col := range []string{"business_profile", "locale_profiles"} {
		a, b := dump(src, `SELECT `+col+`::text FROM hatamoto_site_config`), dump(dst, `SELECT `+col+`::text FROM hatamoto_site_config`)
		if len(a) < 100 || a != b {
			t.Errorf("%s differs or is empty after import:\n src: %s\n dst: %s", col, a, b)
		}
	}
	srcSite, err := bizprofile.Parse([]byte(dump(src, `SELECT business_profile::text FROM hatamoto_site_config`)))
	if err != nil {
		t.Fatal(err)
	}
	dstSite, err := bizprofile.Parse([]byte(dump(dst, `SELECT business_profile::text FROM hatamoto_site_config`)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(srcSite, dstSite) || srcSite.VATID != "VAT-0001" || srcSite.EmployeesMax != 50 || !srcSite.LogoObject ||
		srcSite.OrgAddress != bizprofile.OrgAddressLocaleOrSite || srcSite.Rating.CountType != "reviews" || srcSite.ContactPoints[0].AvailableLanguage != "English" {
		t.Errorf("business_profile decodes differently: src %+v dst %+v", srcSite, dstSite)
	}
	srcCards, err := bizprofile.ParseLocales([]byte(dump(src, `SELECT locale_profiles::text FROM hatamoto_site_config`)))
	if err != nil {
		t.Fatal(err)
	}
	dstCards, err := bizprofile.ParseLocales([]byte(dump(dst, `SELECT locale_profiles::text FROM hatamoto_site_config`)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(srcCards, dstCards) || srcCards["en-XX"].VATID != "VAT-XX" || srcCards["en-XX"].CountryName != "Exampleland" || srcCards["en-XX"].Image == "" {
		t.Errorf("locale_profiles decode differently: src %+v dst %+v", srcCards, dstCards)
	}
	if h := dump(dst, `SELECT password_hash FROM hatamoto_users`); h != "!reset-required" {
		t.Errorf("imported password hash = %q", h)
	}
	for name, want := range map[string][]byte{"tenant/pic.png": mediaBytes, "tenant/pic_thumbnail.png": variantBytes, "tenant/w.webp": webpBytes, "tenant/w_thumbnail.webp": jpegVariant} {
		if got, err := os.ReadFile(filepath.Join(dstMedia, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("media %s differs after import (%v)", name, err)
		}
	}

	// The same URLs render the same pages from both databases.
	paths := []string{"/", "/services", "/local-seo", "/blog/hello-post", "/old-services", "/sitemap.xml", "/sitemap-blog.xml", "/sitemap-misc.xml", "/blog", "/llms.txt", "/media/tenant/pic.png"}
	want := renderKeyURLs(t, srcDSN, srcMedia, paths)
	got := renderKeyURLs(t, dstDSN, dstMedia, paths)
	for _, p := range paths {
		if want[p] != got[p] {
			t.Errorf("%s renders differently:\n source: %.300s\n import: %.300s", p, want[p], got[p])
		}
	}
	for _, p := range []string{"/", "/services", "/local-seo", "/blog/hello-post", "/sitemap-blog.xml", "/llms.txt", "/media/tenant/pic.png"} {
		if !strings.HasPrefix(want[p], "OK") {
			t.Errorf("%s does not render in the source: %.200s", p, want[p])
		}
	}
}

// An archive from a newer format version is refused with a clear message.
func TestImportRejectsUnknownVersion(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := `{"format":"hatamoto-export","version":99,"tables":[]}`
	_ = tw.WriteHeader(&tar.Header{Name: "format.json", Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	_, err := portability.Import(context.Background(), nil, &buf, portability.ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("err = %v", err)
	}
}
