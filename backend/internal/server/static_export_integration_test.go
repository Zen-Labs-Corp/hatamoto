//go:build integration

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/staticexport"
)

// xpSite is a seeded site and a server on it. The site URL comes only from
// the Site settings row (PUBLIC_BASE_URL is unset), and the admin lives at a
// random address.
type xpSite struct {
	srv       *Server
	db        *database.DB
	sfx       string
	host      string
	admin     string
	base      string // /xp-<sfx>
	mediaDir  string
	dataDir   string
	redirect  uuid.UUID
	draftSlug string
	schedSlug string
	orphan    string // unreferenced media file name
	upload    string // form upload file name
}

func newXPSite(t *testing.T, accessMode string) *xpSite {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping static export integration test (needs a migrated Postgres)")
	}
	x := &xpSite{sfx: uuid.NewString()[:6], mediaDir: t.TempDir(), dataDir: t.TempDir()}
	x.host = "xp-" + x.sfx + ".example.com"
	x.admin = "/k3x9-" + x.sfx
	x.base = "/xp-" + x.sfx
	x.draftSlug, x.schedSlug = "draft-"+x.sfx, "later-"+x.sfx
	x.orphan, x.upload = "orphan-"+x.sfx+".jpg", "upload-"+x.sfx+".pdf"

	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "xp-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Static IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Static IT")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", x.mediaDir)
	t.Setenv("HATAMOTO_DATA", x.dataDir)
	t.Setenv("HATAMOTO_WORKER", "off")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.Resolved = x.admin
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

	// Files: the referenced image and its variants, an unreferenced image and
	// a form upload (stored outside the media tree).
	img := new(bytes.Buffer)
	if err := jpeg.Encode(img, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hero-" + x.sfx + ".jpg", "hero-" + x.sfx + "-300.jpg", "hero-" + x.sfx + "-1024.jpg", x.orphan} {
		if err := os.WriteFile(filepath.Join(x.mediaDir, name), img.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	uploads := filepath.Join(x.dataDir, "private", "form-uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, x.upload), []byte("%PDF-1.4 private"), 0o644); err != nil {
		t.Fatal(err)
	}
	hero := "/media/hero-" + x.sfx + ".jpg"
	exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,width,height,variants)
		VALUES ($1,$1,'image/jpeg',$2,$1,$3,1200,800,$4::jsonb)`, "hero-"+x.sfx+".jpg", img.Len(), hero,
		`[{"size":"medium","path":"hero-`+x.sfx+`-300.jpg","url":"/media/hero-`+x.sfx+`-300.jpg","width":300,"height":200},`+
			`{"size":"large","path":"hero-`+x.sfx+`-1024.jpg","url":"/media/hero-`+x.sfx+`-1024.jpg","width":1024,"height":683}]`)
	exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,width,height)
		VALUES ($1,$1,'image/jpeg',$2,$1,$3,8,8)`, x.orphan, img.Len(), "/media/"+x.orphan)

	// Content: two published posts, a draft, a scheduled post, a category, a
	// form linked from a post, and two redirects.
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Static IT posts',$2,'{"type":"object"}',$3)`,
		schemaID, "xp-"+x.sfx, `{"url_pattern":"`+x.base+`/{slug}","archive_path":"`+x.base+`","archive_title":"Static IT"}`)
	formSlug := "contact-" + x.sfx
	post := func(slug, name, status, when, attrs string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,$5,`+when+`,$6::jsonb)`, id, schemaID, name, slug, status, attrs)
		return id
	}
	p1 := post("p1", "First post", "published", "now() - interval '2 hours'",
		`{"content":"<p>First.</p>","featured_image":"`+hero+`"}`)
	p2 := post("p2", "Second post", "published", "now() - interval '1 hour'",
		`{"content":"<p>Second. <a href=\"/f/`+formSlug+`\">Write to us</a></p>"}`)
	post(x.draftSlug, "Hidden draft", "draft", "NULL", `{"content":"<p>draft</p>"}`)
	post(x.schedSlug, "Hidden scheduled", "scheduled", "now() + interval '30 days'", `{"content":"<p>later</p>"}`)
	termID := uuid.New()
	exec(`INSERT INTO hatamoto_terms (id,kind,slug,name) VALUES ($1,'category',$2,'Static IT news')`, termID, "news-"+x.sfx)
	for _, id := range []uuid.UUID{p1, p2} {
		exec(`INSERT INTO hatamoto_entity_terms (entity_id,term_id) VALUES ($1,$2)`, id, termID)
	}
	exec(`INSERT INTO hatamoto_forms (name,slug,fields,settings,status) VALUES ('Static IT form',$1,$2::jsonb,'{}'::jsonb,'active')`,
		formSlug, `[{"name":"email","label":"Email","type":"email","required":true,"position":1}]`)
	exec(`INSERT INTO hatamoto_redirects (source_path,target_url,status_code) VALUES ($1,$2,301)`, "/old-"+x.sfx, x.base+"/p1")
	x.redirect = uuid.Nil
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_redirects WHERE source_path=$1`, "/old-"+x.sfx).Scan(&x.redirect); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_redirects (source_path,target_url,status_code) VALUES ($1,$2,301)`, "/legacy-"+x.sfx+"/*", x.base+"/$1")

	// The home page: a page entity the Site settings point at.
	homeID := uuid.New()
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1, id, 'Static IT home', $2, 'published', now(), $3::jsonb FROM hatamoto_entity_schemas WHERE slug='page'`,
		homeID, "home-"+x.sfx, `{"content":"<p>Welcome. <a href=\"`+x.base+`\">The posts</a></p>"}`)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id=$1`, homeID)
	})

	// Site settings: the only place the site URL is set.
	var hadSite bool
	_ = db.Pool().QueryRow(ctx, `SELECT true FROM hatamoto_site_config`).Scan(&hadSite)
	if hadSite {
		var prevURL, prevMode string
		var prevHome *uuid.UUID
		if err := db.Pool().QueryRow(ctx, `SELECT public_base_url, site_access_mode, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevURL, &prevMode, &prevHome); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(context.Background(), `UPDATE hatamoto_site_config SET public_base_url=$1, site_access_mode=$2, homepage_entity_id=$3`, prevURL, prevMode, prevHome)
		})
		exec(`UPDATE hatamoto_site_config SET public_base_url=$1, site_access_mode=$2, homepage_entity_id=$3`, "https://"+x.host, accessMode, homeID)
	} else {
		exec(`INSERT INTO hatamoto_site_config (company_name, public_base_url, site_access_mode, homepage_entity_id) VALUES ('Static IT',$1,$2,$3)`, "https://"+x.host, accessMode, homeID)
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_site_config`) })
	}
	_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_static_export_runs WHERE trigger = 'test'`)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_static_export_runs WHERE trigger = 'test'`)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path IN ($1,$2)`, "/old-"+x.sfx, "/legacy-"+x.sfx+"/*")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_forms WHERE slug=$1`, formSlug)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_terms WHERE term_id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE id=$1`, termID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_media WHERE filename LIKE $1 OR filename=$2`, "hero-"+x.sfx+"%", x.orphan)
	})

	x.db = db
	x.srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return x
}

// export runs a dir export into a fresh directory and returns it.
func (x *xpSite) export(t *testing.T) (out string, res staticexport.Result, err error) {
	t.Helper()
	out = filepath.Join(t.TempDir(), "out")
	tgt, terr := staticexport.NewDirTarget(out, staticexport.DirGuard{Forbidden: []string{x.dataDir, x.mediaDir}})
	if terr != nil {
		t.Fatal(terr)
	}
	res, err = x.srv.StaticExport(context.Background(), tgt, staticexport.Options{Trigger: "test"})
	return out, res, err
}

// files lists the output tree as "/"-separated paths.
func xpFiles(t *testing.T, out string) []string {
	t.Helper()
	var all []string
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		all = append(all, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// liveGet is an anonymous GET through the router, with no special headers.
func (x *xpSite) liveGet(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	x.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://"+x.host+path, nil))
	return rec
}

// urlOf maps an output file back to the URL path it was fetched at.
func urlOf(file string) string {
	switch {
	case file == "index.html":
		return "/"
	case strings.HasSuffix(file, "/index.html"):
		return "/" + strings.TrimSuffix(file, "/index.html")
	}
	return "/" + file
}

func has(files []string, want string) bool {
	for _, f := range files {
		if f == want {
			return true
		}
	}
	return false
}

func TestStaticExport_A1_EqualsLive(t *testing.T) {
	x := newXPSite(t, "open")
	out, _, err := x.export(t)
	if err != nil {
		t.Fatal(err)
	}
	files := xpFiles(t, out)
	checked := xpCheckEqualsLive(t, x, out, files)
	if checked < 10 {
		t.Fatalf("only %d files compared: %v", checked, files)
	}

	// The inventory.
	for _, want := range []string{
		"index.html", "sitemap.xml", "sitemap.xsl", "robots.txt", "llms.txt", "feed.xml", "atom.xml",
		"api/v1/public/knowledge-graph", "404.html", staticexport.RedirectsFile, staticexport.ManifestFile,
		strings.TrimPrefix(x.base, "/") + "/index.html",
		strings.TrimPrefix(x.base, "/") + "/p1/index.html",
		strings.TrimPrefix(x.base, "/") + "/p2/index.html",
		"media/hero-" + x.sfx + ".jpg", "media/hero-" + x.sfx + "-300.jpg", "media/hero-" + x.sfx + "-1024.jpg",
		"f/contact-" + x.sfx + "/index.html", "category/news-" + x.sfx + "/index.html",
		"old-" + x.sfx + "/index.html",
	} {
		if !has(files, want) {
			t.Errorf("output misses %s", want)
		}
	}
	// Media come from references: the post page names the image and both copies.
	p1, err := os.ReadFile(filepath.Join(out, strings.TrimPrefix(x.base, "/"), "p1", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"hero-" + x.sfx + ".jpg", "hero-" + x.sfx + "-300.jpg", "hero-" + x.sfx + "-1024.jpg"} {
		if !bytes.Contains(p1, []byte(ref)) {
			t.Errorf("the post page does not reference %s", ref)
		}
	}
	var sitemapChild, theme bool
	for _, f := range files {
		sitemapChild = sitemapChild || (strings.HasPrefix(f, "sitemap-") && strings.HasSuffix(f, ".xml"))
		theme = theme || strings.HasPrefix(f, "theme/assets/")
	}
	if !sitemapChild || !theme {
		t.Errorf("sitemap children = %v, theme assets = %v", sitemapChild, theme)
	}

	// Redirects: a stub to the absolute target, and _redirects with the exact
	// rule before the pattern.
	stub, err := os.ReadFile(filepath.Join(out, "old-"+x.sfx, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<link rel="canonical" href="https://` + x.host + x.base + `/p1">`; !bytes.Contains(stub, []byte(want)) {
		t.Errorf("stub = %s", stub)
	}
	rd, err := os.ReadFile(filepath.Join(out, staticexport.RedirectsFile))
	if err != nil {
		t.Fatal(err)
	}
	exact := strings.Index(string(rd), "/old-"+x.sfx+" "+x.base+"/p1 301")
	pat := strings.Index(string(rd), "/legacy-"+x.sfx+"/:p1 "+x.base+"/:p1 301")
	if exact < 0 || pat < 0 || exact > pat {
		t.Errorf("_redirects = %q (exact at %d, pattern at %d)", rd, exact, pat)
	}
}

var listingFileRe = regexp.MustCompile(`^(.+/)?page/(\d+)/index\.html$`)

// xpCheckEqualsLive compares every exported file with what the live site
// answers and returns how many pages it compared. A listing page file is
// compared with its ?page=N page, and every body with the pager links
// rewritten, the only change an export makes to a page.
func xpCheckEqualsLive(t *testing.T, x *xpSite, out string, files []string) (checked int) {
	t.Helper()
	for _, f := range files {
		if f == staticexport.ManifestFile || f == staticexport.RedirectsFile || f == "404.html" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		p := urlOf(f)
		live := x.liveGet(p)
		if m := listingFileRe.FindStringSubmatch(f); m != nil && live.Code == http.StatusNotFound {
			p = urlOf(m[1]+"index.html") + "?page=" + m[2]
			live = x.liveGet(p)
		}
		if live.Code >= 300 && live.Code < 400 {
			if !bytes.Contains(got, []byte(`http-equiv="refresh"`)) {
				t.Errorf("%s: live answers %d but the file is not a redirect stub", f, live.Code)
			}
			continue
		}
		if live.Code != http.StatusOK {
			t.Errorf("%s: live answers %d", p, live.Code)
			continue
		}
		checked++
		doc, err := url.Parse("https://" + x.host + p)
		if err != nil {
			t.Fatal(err)
		}
		if want := staticexport.RewriteListingLinks(doc, x.admin, live.Body.Bytes()); !bytes.Equal(got, want) {
			off := 0
			for off < len(got) && off < len(want) && got[off] == want[off] {
				off++
			}
			t.Errorf("%s differs from the live body (%d vs %d bytes), first difference at byte %d", p, len(got), len(want), off)
		}
	}
	return checked
}

func TestStaticExport_A4_ListingPages(t *testing.T) {
	x := newXPSite(t, "open")
	ctx := context.Background()
	var schemaID uuid.UUID
	if err := x.db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug=$1`, "xp-"+x.sfx).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	// 2 posts exist; 25 more make 27, three pages of 12.
	for i := range 25 {
		if _, err := x.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,'published', now() - make_interval(mins => $5), '{"content":"<p>x</p>"}'::jsonb)`,
			uuid.New(), schemaID, fmt.Sprintf("Listing post %d", i), fmt.Sprintf("lp-%d", i), 10+i); err != nil {
			t.Fatal(err)
		}
	}
	if live := x.liveGet(x.base); !strings.Contains(live.Body.String(), "?page=2") {
		t.Fatalf("the live listing has no ?page=2 link (status %d)", live.Code)
	}

	out, res, err := x.export(t)
	if err != nil {
		t.Fatal(err)
	}
	files := xpFiles(t, out)
	dir := strings.TrimPrefix(x.base, "/")
	for _, want := range []string{dir + "/page/2/index.html", dir + "/page/3/index.html"} {
		if !has(files, want) {
			t.Errorf("output misses %s", want)
		}
	}
	if has(files, dir+"/page/4/index.html") {
		t.Error("output has a page 4")
	}
	first, err := os.ReadFile(filepath.Join(out, dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte(`href="`+x.base+`/page/2/"`)) {
		t.Errorf("the first page does not link /page/2/:\n%s", first)
	}
	second, err := os.ReadFile(filepath.Join(out, dir, "page", "2", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`<link[^>]*rel="canonical"[^>]*\?page=2|<link[^>]*\?page=2[^>]*rel="canonical"`).Match(second) {
		t.Errorf("page 2 lost its live canonical:\n%s", second)
	}
	if res.Report.ListingPages != 2 {
		t.Errorf("ListingPages = %d, want 2", res.Report.ListingPages)
	}
	for _, w := range res.Report.Warnings {
		if strings.Contains(w, "?page=N") {
			t.Errorf("warning %q", w)
		}
	}
	if n := xpCheckEqualsLive(t, x, out, files); n < 10 {
		t.Errorf("only %d files compared", n)
	}
}

func TestStaticExport_A1_ExcludedAbsent(t *testing.T) {
	x := newXPSite(t, "open")
	out, _, err := x.export(t)
	if err != nil {
		t.Fatal(err)
	}
	adminSeg := strings.TrimPrefix(x.admin, "/")
	for _, f := range xpFiles(t, out) {
		for _, bad := range []string{x.draftSlug, x.schedSlug, x.orphan, x.upload} {
			if strings.Contains(f, bad) {
				t.Errorf("%s must not be exported", f)
			}
		}
		for _, prefix := range []string{adminSeg + "/", "auth/", "preview/", "-/"} {
			if strings.HasPrefix(f, prefix) {
				t.Errorf("%s is under an excluded prefix", f)
			}
		}
		if strings.HasPrefix(f, "api/") && f != "api/v1/public/knowledge-graph" {
			t.Errorf("%s is under api/", f)
		}
		body, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(x.admin)) {
			t.Errorf("%s contains the admin address", f)
		}
		for _, slug := range []string{x.draftSlug, x.schedSlug} {
			if bytes.Contains(body, []byte(slug)) {
				t.Errorf("%s mentions the unpublished %s", f, slug)
			}
		}
	}
}

func TestStaticExport_NoSideEffects(t *testing.T) {
	x := newXPSite(t, "open")
	ctx := context.Background()
	count := func(q string, args ...any) int {
		var n int
		if err := x.db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	hits := func() int { return count(`SELECT COALESCE(SUM(hit_count),0) FROM hatamoto_redirects`) }
	log404 := func() int { return count(`SELECT count(*) FROM hatamoto_404_log`) }
	agents := func() int { return count(`SELECT count(*) FROM hatamoto_agent_hits`) }
	h0, l0, a0 := hits(), log404(), agents()

	if _, _, err := x.export(t); err != nil {
		t.Fatal(err)
	}
	// Counters are written in the background; Close drains and flushes them,
	// so a hit the export queued would be in the database below.
	x.srv.redirectService.Close()
	if h, l, a := hits(), log404(), agents(); h != h0 || l != l0 || a != a0 {
		t.Fatalf("export changed counters: redirect hits %d->%d, 404 log %d->%d, agent hits %d->%d", h0, h, l0, l, a0, a)
	}
}

func TestStaticExport_GateRefused(t *testing.T) {
	x := newXPSite(t, "password")
	out, _, err := x.export(t)
	if !errors.Is(err, staticexport.ErrSiteAccessOn) {
		t.Fatalf("err = %v, want ErrSiteAccessOn", err)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatalf("out exists after a refused export: %v", serr)
	}
	var status, errs string
	if err := x.db.Pool().QueryRow(context.Background(),
		`SELECT status, errors::text FROM hatamoto_static_export_runs WHERE trigger='test' ORDER BY started_at DESC LIMIT 1`).Scan(&status, &errs); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(errs, "site access gate is on") {
		t.Fatalf("run = %s %s", status, errs)
	}
}

func TestStaticExport_RunRecorded(t *testing.T) {
	x := newXPSite(t, "open")
	_, res, err := x.export(t)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var pages, assets, media, changed int
	if err := x.db.Pool().QueryRow(context.Background(),
		`SELECT status, pages, assets, media, changed FROM hatamoto_static_export_runs WHERE id=$1`, res.RunID).Scan(&status, &pages, &assets, &media, &changed); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || pages == 0 || pages != res.Report.Pages || assets != res.Report.Assets || media != res.Report.Media || changed != len(res.Diff.Changed) {
		t.Fatalf("run = %s pages=%d assets=%d media=%d changed=%d, result = %+v", status, pages, assets, media, changed, res.Report)
	}
	if media < 3 {
		t.Fatalf("media = %d, want the image and its two variants", media)
	}
}

// A served site's Test export writes the saved directory target, records a
// run marked "test" and leaves the process snapshot (what visitors get) alone.
func TestStaticExport_TestExportInServedMode(t *testing.T) {
	x := newXPSite(t, "open")
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "public")
	set := staticexport.Defaults()
	set.DirPath = dir
	set.LiveOrigin = "https://live.example.net"
	store := staticexport.NewSettingsStore(x.db.Pool())
	if err := store.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = x.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_static_hosting_settings`) })

	h := &hostingAdmin{
		settings: store, runs: staticexport.NewRunStore(x.db.Pool()), key: staticexport.SecretKey("it-secret"),
		siteURL: pluginEntityPaths{x.srv}.BaseURL, testExport: x.srv.StaticTestExport,
		background: func() context.Context { return ctx },
		dirGuard:   staticexport.DirGuard{Forbidden: []string{x.dataDir, x.mediaDir}},
		spawn:      func(f func()) { f() },
	}
	before := x.srv.staticHosting()
	if err := h.TestExport(ctx); err != nil {
		t.Fatal(err)
	}
	if after := x.srv.staticHosting(); after != before || after.Static {
		t.Errorf("snapshot changed: %+v -> %+v", before, after)
	}
	if files := xpFiles(t, dir); len(files) == 0 {
		t.Fatal("test export wrote nothing to the saved directory")
	}
	runs, err := h.runs.List(ctx, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v %v", runs, err)
	}
	if runs[0].Trigger != "test" || runs[0].Status != "ok" {
		t.Errorf("run = %s %s %v", runs[0].Trigger, runs[0].Status, runs[0].Errors)
	}
}
