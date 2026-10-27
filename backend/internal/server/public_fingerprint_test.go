//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/user"
)

var (
	versionRe = regexp.MustCompile(`v?\d+\.\d+\.\d+`)
	assetRe   = regexp.MustCompile(`(?:href|src)="([^"]+)"`)
)

// assertNoFingerprint fails when a public response says what serves it.
func assertNoFingerprint(t *testing.T, name string, rec *httptest.ResponseRecorder, errorBody bool) {
	t.Helper()
	h := rec.Header()
	for _, k := range []string{"Server", "X-Powered-By", "X-Hatamoto-Renderer"} {
		if v := h.Get(k); v != "" {
			t.Errorf("%s: header %s=%q", name, k, v)
		}
	}
	for k, vs := range h {
		if strings.Contains(strings.ToLower(k), "hatamoto") {
			t.Errorf("%s: header name %q", name, k)
		}
		for _, v := range vs {
			if strings.Contains(strings.ToLower(v), "hatamoto") {
				t.Errorf("%s: header %s value %q names hatamoto", name, k, v)
			}
		}
	}
	for _, c := range h.Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(c), "hatamoto") {
			t.Errorf("%s: Set-Cookie %q", name, c)
		}
	}
	for _, v := range h.Values("Cross-Origin-Opener-Policy") {
		if strings.EqualFold(strings.TrimSpace(v), "same-origin") {
			t.Errorf("%s: Cross-Origin-Opener-Policy %q", name, v)
		}
	}
	if errorBody {
		body := rec.Body.String()
		if strings.Contains(strings.ToLower(body), "hatamoto") {
			t.Errorf("%s: body names hatamoto: %.200s", name, body)
		}
		if versionRe.MatchString(body) {
			t.Errorf("%s: body carries a version string: %.200s", name, body)
		}
	}
}

func TestPublicFingerprintIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "fp-it")
	t.Setenv("HATAMOTO_SITE_NAME", "FP IT")
	t.Setenv("PUBLIC_BASE_URL", "https://fp-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("ENABLE_COOKIE_AUTH", "true")
	t.Setenv("ADMIN_COOKIE_SECURE", "false")
	t.Setenv("CSP_ENFORCE", "true")
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
	schemaID, homeID, formID := uuid.New(), uuid.New(), uuid.New()
	formSlug := "fp-form-" + sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "fpit-"+sfx, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Home FP',$3,'published',now(),$4::jsonb)`, homeID, schemaID, "home-"+sfx,
		`{"page_sections":[{"id":"s1","type":"hero","sort_order":1,"content":{"headline":"Hello"}},
		 {"id":"c1","type":"contact_form","sort_order":2,"content":{"formSlug":"`+formSlug+`","heading":"Write us"}}]}`)
	exec(`INSERT INTO hatamoto_forms (id,name,slug,fields,settings,status) VALUES ($1,'FP form',$2,$3::jsonb,'{}','active')`, formID, formSlug,
		`[{"name":"email","label":"Email","type":"email","required":true,"position":1}]`)
	var prevName string
	var prevHome *uuid.UUID
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevName, &prevHome) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('FP IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, homeID)

	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	email := "fp-" + sfx + "@example.test"
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: email, Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET homepage_entity_id=$1`, prevHome)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_admin_audit WHERE actor_id=$1`, owner.ID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_forms WHERE id=$1`, formID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})

	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}

	home := do(http.MethodGet, "/", "", nil)
	if home.Code != 200 {
		t.Fatalf("/: %d", home.Code)
	}
	html := home.Body.String()

	// ISC-20: no generator meta by default.
	if regexp.MustCompile(`(?i)<meta[^>]+name=["']?generator`).MatchString(html) {
		t.Error("default page carries a generator meta tag")
	}

	// Asset URLs in the HTML never name the product.
	var cssPath string
	for _, m := range assetRe.FindAllStringSubmatch(html, -1) {
		if strings.Contains(strings.ToLower(m[1]), "hatamoto") {
			t.Errorf("asset URL %q names hatamoto", m[1])
		}
		if cssPath == "" && strings.HasPrefix(m[1], "/theme/assets/") && strings.HasSuffix(m[1], ".css") {
			cssPath = m[1]
		}
	}
	if cssPath == "" {
		t.Error("no theme CSS URL found in the homepage")
		cssPath = "/theme/assets/hm.js"
	}

	// Plugin assets (/p/<name>/assets/...) need an installed plugin; the unknown
	// plugin route is covered below as an error body instead.
	rows := []struct {
		name, method, path string
		errorBody          bool
	}{
		{"homepage", "GET", "/", false},
		{"404 page", "GET", "/nope-" + sfx, true},
		{"theme css", "GET", cssPath, false},
		{"hm.js", "GET", "/theme/assets/hm.js", false},
		{"health", "GET", "/health", true},
		{"ready", "GET", "/ready", true},
		{"robots", "GET", "/robots.txt", false},
		{"sitemap", "GET", "/sitemap.xml", false},
		{"form token default prefix", "GET", "/api/v1/public/forms/" + formSlug + "/token", false},
		{"public api 404", "GET", "/api/v1/public/nope-" + sfx, true},
		{"api 404", "GET", "/api/v1/nope-" + sfx, true},
		{"plugin asset 404", "GET", "/p/nope-" + sfx + "/assets/x.js", true},
	}
	for _, r := range rows {
		rec := do(r.method, r.path, "", nil)
		assertNoFingerprint(t, r.name, rec, r.errorBody)
	}
	// The configured public API prefix serves the same endpoints without a fingerprint,
	// and hm.js calls it.
	t.Cleanup(func() { render.SetPublicAPIPrefix("") })
	t.Setenv("PUBLIC_API_PREFIX", "/x/v1")
	cfg2, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv2 := New(cfg2, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, r := range []struct{ name, method, path, body string }{
		{"form token custom prefix", "GET", "/x/v1/forms/" + formSlug + "/token", ""},
		{"track custom prefix", "POST", "/x/v1/track", `{"path":"/"}`},
		{"custom prefix 404", "GET", "/x/v1/nope-" + sfx, ""},
		{"hm.js custom prefix", "GET", "/theme/assets/hm.js", ""},
	} {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv2.router.ServeHTTP(rec, req)
		if rec.Code >= 500 {
			t.Errorf("%s: %d", r.name, rec.Code)
		}
		assertNoFingerprint(t, r.name, rec, strings.HasSuffix(r.name, "404"))
		if r.name == "hm.js custom prefix" && !strings.Contains(rec.Body.String(), "/x/v1/forms/") {
			t.Error("hm.js does not call the configured prefix")
		}
	}

	// Health bodies are the plain word.
	for _, p := range []string{"/health", "/ready"} {
		if rec := do("GET", p, "", nil); rec.Body.String() != "ok" {
			t.Errorf("%s body %q", p, rec.Body)
		}
	}

	// Login under neutral cookie names: only neutral Set-Cookie names appear.
	t.Cleanup(func() {
		auth.SessionCookieName, auth.APISessionCookieName = "hatamoto_session", "hatamoto_api_session"
		middleware.CSRFCookieName = "hatamoto_csrf"
	})
	auth.UseNeutralCookieNames(false)
	middleware.UseNeutralCSRFCookieName(false)
	login := do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"a-long-enough-password"}`,
		map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	assertNoFingerprint(t, "login", login, false)
	got := map[string]bool{}
	for _, c := range login.Result().Cookies() {
		got[c.Name] = true
	}
	if !got["a"] || len(got) != 1 {
		t.Errorf("login cookie names = %v, want exactly a", got)
	}
}
