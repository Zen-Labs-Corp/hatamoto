//go:build integration

package server

import (
	"bytes"
	"context"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/sdk"
)

// A plugin that asks ui.allow camera gets the camera on its admin page only
// after the owner grants it. The grant opens the camera to the admin page
// (camera=(self)) and leaves the plugin's own document (sandbox, CSP, headers)
// alone; dropping the ask or uninstalling ends it.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PluginCameraIntegration
func TestPluginCameraIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "cam-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Cam IT")
	t.Setenv("PUBLIC_BASE_URL", "https://cam-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_REGISTRY_URL", "off")
	t.Setenv("HATAMOTO_TRUSTED_KEYS", key.Public)
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
	reset := func() {
		for _, q := range []string{`DELETE FROM hatamoto_packages WHERE name = 'camp'`, `DELETE FROM hatamoto_plugin_grants WHERE plugin = 'camp'`} {
			_, _ = db.Pool().Exec(context.Background(), q)
		}
	}
	reset()
	t.Cleanup(reset)
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}

	hash, _ := auth.HashPassword("a-long-enough-password")
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "cam-" + uuid.NewString()[:6] + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})
	completeProfile(t, srv, owner)
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	csrf := adminToken(t, srv.router, token)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		adminReq(req, token, csrf)
		if strings.HasPrefix(req.URL.Path, "/admin") || strings.HasPrefix(req.URL.Path, "/plugin-ui/") {
			navigation(req)
		}
		if strings.HasPrefix(req.URL.Path, "/plugin-ui/") {
			req.Header.Set("Sec-Fetch-Dest", "iframe")
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	postForm := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		adminReq(req, token, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	upload := func(data []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf", csrf)
		fw, _ := mw.CreateFormFile("package", "x.hmp")
		_, _ = fw.Write(data)
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/admin/plugins/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		adminReq(req, token, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	pack := func(version string, allow []string) []byte {
		mjson, _ := jsonx.Marshal(sdk.Manifest{Name: "camp", Version: version, SDK: "^0.22.2", Permissions: []string{sdk.PermKV},
			UI: &sdk.AdminUI{Title: "Camp board", Allow: allow}})
		data, err := packages.PackFiles(map[string][]byte{
			packages.PluginManifest: mjson, packages.PluginWasm: plugintest.Build(t, "capa"),
			"ui/index.html": []byte(`<!doctype html><script src="/plugin-ui/_sdk/bridge.js"></script><h1>camp</h1>`),
		}, &key)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	install := func(data []byte) {
		t.Helper()
		rec := upload(data)
		m := regexp.MustCompile(`name="staged" value="([^"]+)"`).FindStringSubmatch(rec.Body.String())
		if rec.Code != 200 || m == nil {
			t.Fatalf("upload: %d\n%s", rec.Code, rec.Body.String())
		}
		// Only kv is posted: the camera is never a hidden grant.
		if rec := postForm("/admin/plugins/install", url.Values{"staged": {m[1]}, "permission": {"kv"}}); rec.Code != 303 {
			t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
		}
	}
	grants := func() []string {
		return srv.plugins.Grants().Granted("camp")
	}
	hasCamera := func(body string) bool {
		return strings.Contains(body, ` data-camera="1"`) || strings.Contains(body, `id="plugin-camera"`)
	}
	const hostPolicy = "geolocation=(), camera=(self), microphone=()"
	const globalPolicy = "geolocation=(), camera=(), microphone=()"

	// (a) installed with the ask but no grant: today's page and headers.
	install(pack("1.0.0", []string{sdk.UIAllowCamera}))
	page := get("/admin/plugins/camp/settings")
	if page.Code != 200 || hasCamera(page.Body.String()) || page.Header().Get("Permissions-Policy") != globalPolicy {
		t.Fatalf("ungranted: %d policy=%q camera=%v", page.Code, page.Header().Get("Permissions-Policy"), hasCamera(page.Body.String()))
	}
	for _, g := range grants() {
		if g == sdk.PermUICamera {
			t.Fatal("installing must not grant the camera")
		}
	}

	// (b) the owner grants it.
	if rec := postForm("/admin/plugins/camp/grant", url.Values{"permission": {"kv", sdk.PermUICamera}}); rec.Code != 303 {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
	}
	page = get("/admin/plugins/camp/settings")
	body := page.Body.String()
	if !hasCamera(body) || page.Header().Get("Permissions-Policy") != hostPolicy {
		t.Fatalf("granted: policy=%q camera=%v", page.Header().Get("Permissions-Policy"), hasCamera(body))
	}
	if !strings.Contains(body, `sandbox="allow-scripts"`) || strings.Contains(body, "allow-same-origin") || strings.Contains(body, `allow="camera`) {
		t.Error("the frame must stay sealed")
	}
	src := regexp.MustCompile(`src="(/plugin-ui/camp/index\.html\?t=[^"]+)"`).FindStringSubmatch(body)
	if src == nil {
		t.Fatalf("no framed document with a ticket:\n%s", body)
	}
	doc := get(html.UnescapeString(src[1]))
	if doc.Code != 200 {
		t.Fatalf("framed document: %d", doc.Code)
	}
	if pp := doc.Header().Get("Permissions-Policy"); strings.Contains(pp, "camera=(self)") || pp != globalPolicy {
		t.Errorf("the plugin's own document must not get the camera: %q", pp)
	}
	const csp = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	if got := doc.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("document CSP changed: %q", got)
	}

	// (c) an update that no longer asks keeps the stored grant but loses the page's camera.
	install(pack("1.1.0", nil))
	page = get("/admin/plugins/camp/settings")
	if page.Code != 200 || hasCamera(page.Body.String()) || page.Header().Get("Permissions-Policy") != globalPolicy {
		t.Errorf("after the ask is dropped: %d policy=%q camera=%v", page.Code, page.Header().Get("Permissions-Policy"), hasCamera(page.Body.String()))
	}

	// (d) uninstall removes the grants.
	if rec := postForm("/admin/plugins/camp/uninstall", url.Values{}); rec.Code != 303 {
		t.Fatalf("uninstall: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_plugin_grants WHERE plugin = 'camp'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("grants after uninstall: %d %v", n, err)
	}
}
