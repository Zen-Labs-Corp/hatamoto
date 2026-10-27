//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/wpimport"
	"hatamoto.dev/core/migrations"
)

// The WordPress import wizard, end to end through the whole server (session,
// CSRF, CSP, background job, in-process URL audit), against the seeded
// WordPress of test/wpfixture and a scratch database it migrates itself on the Postgres of DATABASE_URL:
//
//	test/wpfixture/run-local.sh up
//	set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a
//	DATABASE_URL=... go test -tags=integration -run WordPressWizard ./internal/server
func TestWordPressWizardImport_Integration(t *testing.T) {
	siteURL, wpUser, wpPass := os.Getenv("WP_FIXTURE_URL"), os.Getenv("WP_FIXTURE_USER"), os.Getenv("WP_FIXTURE_APP_PASSWORD")
	base := os.Getenv("DATABASE_URL")
	if siteURL == "" || base == "" {
		t.Skip("WP_FIXTURE_URL or DATABASE_URL not set: start test/wpfixture and a Postgres")
	}
	dsn := scratchMigratedDB(t, base)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "wizard-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Wizard IT")
	t.Setenv("PUBLIC_BASE_URL", "https://new-site.example.test")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_WORKER", "off")
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
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	newServer := func() *Server {
		s := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if s.renderErr != nil {
			t.Fatal(s.renderErr)
		}
		t.Cleanup(func() { s.importManager.Close() })
		return s
	}
	srv := newServer()

	// ---- an owner, an editor, and their sessions
	hash, _ := auth.HashPassword("a-long-enough-password")
	sfx := uuid.NewString()[:6]
	mkUser := func(role user.Role) string {
		u, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: string(role) + "-" + sfx + "@example.test", Role: role, Name: string(role)}, hash)
		if err != nil {
			t.Fatal(err)
		}
		completeProfile(t, srv, u)
		tok, err := srv.authService.IssueSession(ctx, u.ID, "it", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	ownerTok, editorTok := mkUser(user.RoleOwner), mkUser(user.RoleEditor)

	var seenMu sync.Mutex
	var bodies []string // every response, to prove the password is never in one
	do := func(s *Server, tok, method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		csrf := adminToken(t, s.router, tok)
		var body io.Reader
		var ctype string
		if form != nil {
			form.Set("_csrf", csrf)
			body, ctype = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
		}
		return send(t, s, tok, method, path, body, ctype, csrf, &seenMu, &bodies)
	}

	// ---- owner only
	if rec := do(srv, editorTok, "GET", "/admin/settings/import/wordpress", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("editor: %d", rec.Code)
	}
	if rec := do(srv, "", "GET", "/admin/settings/import/wordpress", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("signed out: %d", rec.Code)
	}
	if rec := do(srv, ownerTok, "GET", "/admin/settings/import/wordpress", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Scan the site") {
		t.Fatalf("source step: %d", rec.Code)
	}
	if rec := do(srv, ownerTok, "GET", "/admin/profile", nil); !strings.Contains(rec.Body.String(), `href="/admin/settings"`) {
		t.Error("the sidebar does not link Settings")
	}

	// wait polls the run page until done(body) holds.
	wait := func(s *Server, id string, what string, done func(body string) bool) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			rec := do(s, ownerTok, "GET", "/admin/settings/import/wordpress/"+id, nil)
			if rec.Code != 200 {
				t.Fatalf("run page: %d %s", rec.Code, rec.Body.String())
			}
			if b := rec.Body.String(); done(b) {
				return b
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
	h1 := regexp.MustCompile(`<h1>([^<]*)</h1>`)
	title := func(b string) string {
		if m := h1.FindStringSubmatch(b); m != nil {
			return m[1]
		}
		return ""
	}
	begin := func(s *Server, form url.Values) string {
		t.Helper()
		rec := do(s, ownerTok, "POST", "/admin/settings/import/wordpress", form)
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/settings/import/wordpress/") {
			t.Fatalf("begin: %d %s", rec.Code, rec.Body.String())
		}
		return strings.TrimPrefix(loc, "/admin/settings/import/wordpress/")
	}
	restForm := func() url.Values {
		return url.Values{"kind": {"rest"}, "url": {siteURL}, "user": {wpUser}, "password": {wpPass}}
	}
	scanned := func(b string) bool { return title(b) == "Scan report" || title(b) == "Import report" }

	// ---- the private-host override is not on the web: the fixture is on 127.0.0.1
	f := restForm()
	f.Set("allow_private", "1")
	f.Set("allow_private_hosts", "true")
	seeded := count(`SELECT count(*) FROM hatamoto_entities`) // first boot seeds a starter home page
	refused := begin(srv, f)
	b := wait(srv, refused, "the refused scan", func(b string) bool { return title(b) == "Import report" })
	if !strings.Contains(b, "Failed") || !strings.Contains(b, "blocked address") {
		t.Fatalf("a private address was scanned from the web:\n%s", b)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities`); n != seeded {
		t.Fatalf("%d entities written by a refused import", n-seeded)
	}
	t.Logf("web refused the private address: %s", regexp.MustCompile(`role="alert">([^<]*)<`).FindStringSubmatch(b)[1])

	// The tests (and nothing else) may lift the guard on the manager.
	srv.importManager.AllowPrivate = true

	// ---- a WXR upload: multipart, scanned, nothing written
	if wxr := os.Getenv("WP_FIXTURE_WXR"); wxr != "" {
		data, err := os.ReadFile(wxr)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf", adminToken(t, srv.router, ownerTok)) // the token must be the first part: it is never read from the query string
		_ = mw.WriteField("kind", "wxr")
		fw, _ := mw.CreateFormFile("wxr", filepath.Base(wxr))
		_, _ = fw.Write(data)
		_ = mw.Close()
		rec := send(t, srv, ownerTok, "POST", "/admin/settings/import/wordpress", &buf, mw.FormDataContentType(), adminToken(t, srv.router, ownerTok), &seenMu, &bodies)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
		}
		id := strings.TrimPrefix(rec.Header().Get("Location"), "/admin/settings/import/wordpress/")
		b := wait(srv, id, "the WXR scan", scanned)
		if title(b) != "Scan report" || !strings.Contains(b, "Contact Form 7") {
			t.Fatalf("WXR scan:\n%s", b)
		}
		t.Log("WXR upload scanned")
	}
	// An empty file and a missing choice are validation errors, not crashes.
	if rec := do(srv, ownerTok, "POST", "/admin/settings/import/wordpress", url.Values{"kind": {"wxr"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no file: %d", rec.Code)
	}
	if rec := do(srv, ownerTok, "POST", "/admin/settings/import/wordpress", url.Values{"kind": {"rest"}, "url": {siteURL}, "user": {wpUser}}); rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), wpPass) {
		t.Errorf("user without password: %d", rec.Code)
	}

	// ---- Source -> Scan report
	id := begin(srv, restForm())
	if n := count(`SELECT count(*) FROM hatamoto_import_secrets WHERE run_id = $1`, id); n != 1 {
		t.Fatalf("sealed credentials: %d", n)
	}
	var sealed []byte
	_ = db.Pool().QueryRow(ctx, `SELECT sealed FROM hatamoto_import_secrets WHERE run_id = $1`, id).Scan(&sealed)
	if bytes.Contains(sealed, []byte(wpPass)) || bytes.Contains(sealed, []byte(strings.ReplaceAll(wpPass, " ", ""))) {
		t.Fatal("the application password is stored in the clear")
	}
	if n := count(`SELECT count(*) FROM hatamoto_import_runs WHERE options::text LIKE '%' || $1 || '%' OR report::text LIKE '%' || $1 || '%'`, wpPass); n != 0 {
		t.Fatal("the application password is in a run's options or report")
	}
	scan := wait(srv, id, "the scan", scanned)
	for _, want := range []string{"Scan report", "Empty site", "Yoast SEO", "Contact Form 7", "Elementor", "acme_pricing_table", "/{year}/{month}/{slug}", "/%year%/%monthnum%/%postname%/", "portfolio", "Expected fidelity"} {
		if !strings.Contains(scan, want) {
			t.Errorf("scan report lacks %q", want)
		}
	}

	// ---- Options -> Run (live progress) -> Report
	opt := do(srv, ownerTok, "GET", "/admin/settings/import/wordpress/"+id+"/options", nil).Body.String()
	for _, want := range []string{`name="types" value="post"`, `name="types" value="portfolio"`, `name="media"`, `name="patterns"`} {
		if !strings.Contains(opt, want) {
			t.Errorf("options lack %q", want)
		}
	}
	form := url.Values{"types": {"post", "page", "portfolio"}, "menus": {"1"}, "drafts": {"1"}, "media": {"1"}, "patterns": {"wordpress"}}
	if rec := do(srv, ownerTok, "POST", "/admin/settings/import/wordpress/"+id+"/start", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	sawProgress := false
	report := wait(srv, id, "the import", func(b string) bool {
		if strings.Contains(b, `id="import-progress"`) {
			sawProgress = true
			if !strings.Contains(b, "<progress") && !strings.Contains(b, "Reading") {
				t.Errorf("progress page without progress")
			}
			return false
		}
		return title(b) == "Import report"
	})
	t.Logf("saw the live progress page: %v", sawProgress)
	var raw json.RawMessage
	var status string
	if err := db.Pool().QueryRow(ctx, `SELECT status, report FROM hatamoto_import_runs WHERE id = $1`, id).Scan(&status, &raw); err != nil {
		t.Fatal(err)
	}
	var res wpimport.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	t.Logf("run: status=%s created=%d failed=%d media=%d/%d redirects=%d audit=%+v", status, res.Created, res.Failed, res.MediaImported, res.MediaReused, res.Redirects, res.Audit)
	if status != "completed" || !strings.Contains(report, "Completed") {
		t.Fatalf("status %s\n%s", status, report)
	}
	if res.Created == 0 || res.Failed != 0 || len(res.Errors) != 0 || len(res.WPContentLeft) != 0 {
		t.Errorf("result: created=%d failed=%d errors=%+v wp-content=%v", res.Created, res.Failed, res.Errors, res.WPContentLeft)
	}
	// Every old URL answers 200 or exactly one 301, checked in process by the wizard.
	if a := res.Audit; a == nil || a.Checked < 15 || len(a.Failures) != 0 || len(a.Unresolved) != 0 {
		t.Errorf("audit: %+v", a)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE attributes::text LIKE '%wp-content%'`); n != 0 {
		t.Errorf("%d entities still name wp-content", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_import_secrets WHERE run_id = $1`, id); n != 0 {
		t.Error("the credential outlived the completed run")
	}
	// The report links the low-fidelity page (the Elementor landing page) to its edit screen.
	var landing uuid.UUID
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entities WHERE metadata->>'import_key' LIKE 'wp:%' AND lower(name) LIKE '%landing%' LIMIT 1`).Scan(&landing); err != nil {
		t.Fatalf("landing page: %v", err)
	}
	for _, want := range []string{"Pages to review", "/admin/entities/" + landing.String() + "/edit", "URL audit", "Redirects created", "Items that failed"} {
		if want == "Items that failed" {
			continue // none failed
		}
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if n := count(`SELECT count(*) FROM hatamoto_redirects`); n > 0 && !strings.Contains(report, "301 to ") {
		t.Errorf("%d redirects exist but the report lists none", n)
	}
	// The site serves what was imported (patterns and caches were refreshed).
	if rec := send(t, srv, "", "GET", "/services/seo/local", nil, "", "", &seenMu, &bodies); rec.Code != 200 {
		t.Errorf("/services/seo/local on the new site: %d", rec.Code)
	}

	// ---- audit log of the run
	for _, action := range []string{"import.scan", "import.start", "import.completed"} {
		if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = $1 AND target = $2 AND actor LIKE 'owner-%'`, action, "import_run:"+id); n != 1 {
			t.Errorf("audit rows for %s: %d", action, n)
		}
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE detail::text LIKE '%' || $1 || '%'`, wpPass); n != 0 {
		t.Error("the audit log holds the password")
	}

	// ---- re-sync: the Source step is prefilled (never the password) and a re-run adds nothing
	pre := do(srv, ownerTok, "GET", "/admin/settings/import/wordpress?from="+id, nil).Body.String()
	if !strings.Contains(pre, `value="`+siteURL+`"`) || !strings.Contains(pre, `value="`+wpUser+`"`) || strings.Contains(pre, wpPass) {
		t.Errorf("re-sync prefill:\n%s", pre)
	}
	before := count(`SELECT count(*) FROM hatamoto_entities`)
	mapRows := count(`SELECT count(*) FROM hatamoto_import_map`)
	id2 := begin(srv, restForm())
	wait(srv, id2, "the second scan", func(b string) bool { return title(b) == "Scan report" })
	do(srv, ownerTok, "POST", "/admin/settings/import/wordpress/"+id2+"/start", form)
	wait(srv, id2, "the re-sync", func(b string) bool { return title(b) == "Import report" })
	var res2 wpimport.Result
	var r2 json.RawMessage
	_ = db.Pool().QueryRow(ctx, `SELECT report FROM hatamoto_import_runs WHERE id = $1`, id2).Scan(&r2)
	_ = json.Unmarshal(r2, &res2)
	if res2.Created != 0 || res2.Failed != 0 || res2.MediaImported != 0 || count(`SELECT count(*) FROM hatamoto_entities`) != before || count(`SELECT count(*) FROM hatamoto_import_map`) != mapRows {
		t.Errorf("re-sync created=%d failed=%d media=%d entities %d -> %d", res2.Created, res2.Failed, res2.MediaImported, before, count(`SELECT count(*) FROM hatamoto_entities`))
	}

	// ---- a restart: a new server (new manager, no memory of the first) resumes from the stored run
	id3 := begin(srv, restForm()) // scanned on the first server...
	wait(srv, id3, "the third scan", func(b string) bool { return title(b) == "Scan report" })
	srv2 := newServer() // ...started on a fresh one: the source is read again with the sealed credential
	srv2.importManager.AllowPrivate = true
	do(srv2, ownerTok, "POST", "/admin/settings/import/wordpress/"+id3+"/start", form)
	wait(srv2, id3, "the run after a restart", func(b string) bool { return title(b) == "Import report" })
	if s := count(`SELECT count(*) FROM hatamoto_import_runs WHERE id = $1 AND status = 'completed'`, id3); s != 1 {
		t.Error("the run started after a restart did not complete")
	}
	// A run that was "running" when the server died shows as interrupted and resumes.
	id4 := begin(srv2, restForm())
	wait(srv2, id4, "the fourth scan", func(b string) bool { return title(b) == "Scan report" })
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_import_runs SET status = 'running', phase = 'content' WHERE id = $1`, id4); err != nil {
		t.Fatal(err)
	}
	srv3 := newServer()
	srv3.importManager.AllowPrivate = true
	page := do(srv3, ownerTok, "GET", "/admin/settings/import/wordpress/"+id4, nil).Body.String()
	if !strings.Contains(page, "Interrupted") || !strings.Contains(page, "Resume the import") {
		t.Fatalf("interrupted run:\n%s", page)
	}
	if rec := do(srv3, ownerTok, "POST", "/admin/settings/import/wordpress/"+id4+"/resume", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body.String())
	}
	wait(srv3, id4, "the resumed run", func(b string) bool { return title(b) == "Import report" })
	if s := count(`SELECT count(*) FROM hatamoto_import_runs WHERE id = $1 AND status = 'completed'`, id4); s != 1 {
		t.Error("the resumed run did not complete")
	}
	if count(`SELECT count(*) FROM hatamoto_entities`) != before {
		t.Error("resuming duplicated content")
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'import.resume' AND target = $1`, "import_run:"+id4); n != 1 {
		t.Errorf("resume audit rows: %d", n)
	}

	// ---- the password never appeared in any page the owner saw
	seenMu.Lock()
	defer seenMu.Unlock()
	for _, body := range bodies {
		if strings.Contains(body, wpPass) || strings.Contains(body, strings.ReplaceAll(wpPass, " ", "")) {
			t.Fatal("the application password was echoed in a response")
		}
	}
	t.Logf("%d responses checked for the password", len(bodies))
}

// send issues one request against the server's router as the session in tok.
func send(t *testing.T, s *Server, tok, method, path string, body io.Reader, ctype, csrf string, mu *sync.Mutex, bodies *[]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if tok != "" {
		adminReq(req, tok, csrf)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	mu.Lock()
	*bodies = append(*bodies, rec.Body.String())
	mu.Unlock()
	return rec
}

// scratchMigratedDB creates a fresh database next to the one in base, applies
// every core migration to it and returns its address.
func scratchMigratedDB(t *testing.T, base string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_wizard_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return u.String()
}
