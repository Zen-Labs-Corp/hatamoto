//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
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
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/sdk"
)

// Packages through the whole server: a signed plugin and theme are uploaded in
// the admin, go live without a restart, the theme is previewed with the signed
// cookie (never cached), activated, and the plugin's page, assets and bridge work.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PackagesIntegration
func TestPackagesIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "pkg-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Pkg IT")
	t.Setenv("PUBLIC_BASE_URL", "https://pkg-it.example.test")
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
		for _, q := range []string{`DELETE FROM hatamoto_packages`, `DELETE FROM hatamoto_theme_settings`, `UPDATE hatamoto_package_config SET active_theme = NULL, allow_unsigned = false`} {
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
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "pkg-" + uuid.NewString()[:6] + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
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
	send := func(req *http.Request, signedIn bool) *httptest.ResponseRecorder {
		if signedIn {
			adminReq(req, token, csrf)
		}
		if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/admin") {
			navigation(req) // signed out, a browser still navigates
		}
		if strings.HasPrefix(req.URL.Path, "/plugin-ui/") {
			navigation(req)
			req.Header.Set("Sec-Fetch-Dest", "iframe") // the plugin page is only ever framed
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string, signedIn bool, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return send(req, signedIn)
	}
	postForm := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return send(req, true)
	}
	upload := func(data []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf", csrf) // the streamed upload reads the token from the first part
		fw, _ := mw.CreateFormFile("package", "x.hmp")
		_, _ = fw.Write(data)
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/admin/plugins/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return send(req, true)
	}

	// ---- a plugin: upload, permission prompt, confirm
	manifest := sdk.Manifest{Name: "capa", Version: "1.0.0", SDK: "^0.7",
		Permissions: []string{sdk.PermKV, sdk.PermLog, sdk.PermSiteScripts},
		Routes:      []sdk.Route{{Method: "GET", Path: "/echo"}, {Method: "GET", Path: "/who/{name}", Public: true}},
		UI:          &sdk.AdminUI{Title: "Capa board"}}
	mjson, _ := json.Marshal(manifest)
	pluginPkg, err := packages.PackFiles(map[string][]byte{
		packages.PluginManifest: mjson, packages.PluginWasm: plugintest.Build(t, "capa"),
		"assets/plugin.css": []byte(".capa{color:red}"), "assets/plugin.js": []byte("window.capa=1"), "assets/secret.svg": []byte("<svg onload=alert(1)/>"),
		"ui/index.html": []byte(`<!doctype html><script src="/plugin-ui/_sdk/bridge.js"></script><h1>capa</h1>`),
	}, &key)
	if err != nil {
		t.Fatal(err)
	}
	rec := upload(pluginPkg)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Install capa") || !strings.Contains(rec.Body.String(), "<code>kv</code>") {
		t.Fatalf("prompt: %d\n%s", rec.Code, rec.Body.String())
	}
	staged := regexp.MustCompile(`name="staged" value="([^"]+)"`).FindStringSubmatch(rec.Body.String())[1]
	if got := get("/p/capa/assets/plugin.css", false); got.Code != 404 {
		t.Fatalf("before the owner confirms nothing may be live: %d", got.Code)
	}
	if rec := postForm("/admin/plugins/install", url.Values{"staged": {staged}, "permission": {"kv"}}); rec.Code != 303 {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}

	t.Run("plugin is live without a restart", func(t *testing.T) {
		css := get("/p/capa/assets/plugin.css", false)
		if css.Code != 200 || css.Body.String() != ".capa{color:red}" || !strings.Contains(css.Header().Get("Content-Type"), "css") || css.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("asset: %d %q %v", css.Code, css.Body.String(), css.Header())
		}
		if svg := get("/p/capa/assets/secret.svg", false); !strings.Contains(svg.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("an uploaded svg must be sandboxed: %v", svg.Header())
		}
		if get("/p/capa/assets/../plugin.json", false).Code == 200 || get("/p/capa/assets/nope.css", false).Code != 404 {
			t.Error("only assets/ files are public")
		}
		page := get("/definitely-not-a-page", false).Body.String()
		if !strings.Contains(page, `<link rel="stylesheet" href="/p/capa/assets/plugin.css">`) {
			t.Errorf("public pages must carry the plugin's stylesheet:\n%s", page)
		}
		const jsTag = `<script src="/p/capa/assets/plugin.js" defer>`
		if strings.Contains(page, jsTag) {
			t.Errorf("plugin.js must not be linked without the site.scripts grant:\n%s", page)
		}
		if rec := postForm("/admin/plugins/capa/grant", url.Values{"permission": {"kv", "site.scripts"}}); rec.Code != 303 {
			t.Fatalf("grant site.scripts: %d %s", rec.Code, rec.Body.String())
		}
		if page := get("/definitely-not-a-page", false).Body.String(); !strings.Contains(page, jsTag) {
			t.Errorf("plugin.js must be linked once site.scripts is granted:\n%s", page)
		}
		if rec := postForm("/admin/plugins/capa/grant", url.Values{"permission": {"kv"}}); rec.Code != 303 {
			t.Fatalf("revoke site.scripts: %d", rec.Code)
		}
		if strings.Contains(get("/definitely-not-a-page", false).Body.String(), jsTag) {
			t.Error("revoking site.scripts must unlink plugin.js again")
		}
		if who := get("/p/capa/who/ada", false); who.Code != 200 {
			t.Errorf("the plugin's public route: %d %s", who.Code, who.Body.String())
		}
		if g := srv.plugins.Grants().Granted("capa"); len(g) != 1 || g[0] != sdk.PermKV {
			t.Errorf("only the ticked permission is granted: %v", g)
		}
	})

	t.Run("plugin page and bridge", func(t *testing.T) {
		if old := get("/admin/plugins/capa/ui", true); old.Code != 301 || old.Header().Get("Location") != "/admin/plugins/capa/settings" {
			t.Fatalf("the old /ui address: %d %q", old.Code, old.Header().Get("Location"))
		}
		host := get("/admin/plugins/capa/settings", true)
		if host.Code != 200 || !strings.Contains(host.Body.String(), `sandbox="allow-scripts"`) || strings.Contains(host.Body.String(), "allow-same-origin") {
			t.Fatalf("host page: %d\n%s", host.Code, host.Body.String())
		}
		if !strings.Contains(get("/admin/plugins", true).Body.String(), `href="/admin/plugins/capa/settings"`) {
			t.Error("the plugin's page is linked from the admin")
		}
		src := regexp.MustCompile(`src="(/plugin-ui/capa/index\.html\?t=[^"]+)"`).FindStringSubmatch(host.Body.String())
		if src == nil {
			t.Fatalf("the host page carries no framed document with a ticket:\n%s", host.Body.String())
		}
		frameURL := html.UnescapeString(src[1])
		if bare := get("/plugin-ui/capa/index.html", true); bare.Code != 404 {
			t.Errorf("the document without a ticket: %d", bare.Code)
		}
		doc := get(frameURL, true)
		if doc.Code != 200 || !strings.Contains(doc.Header().Get("Content-Security-Policy"), "connect-src 'none'") {
			t.Fatalf("document: %d %v", doc.Code, doc.Header())
		}
		if get(frameURL, true).Code != 404 {
			t.Error("a frame ticket is single use")
		}
		if get("/plugin-ui/capa/index.html", false).Code != 404 {
			t.Error("the document needs a session and a ticket")
		}
		call := func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "/admin/plugins/capa/bridge", strings.NewReader(body))
			return send(req, true)
		}
		rec := call(`{"op":"route","method":"GET","path":"/echo","query":{"a":"1"}}`)
		var rep struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rec.Code != 200 || rep.Status != 201 || !strings.Contains(rep.Body, `"owner"`) {
			t.Fatalf("route through the bridge ran as the signed-in user: %d %s", rec.Code, rec.Body.String())
		}
		rec = call(`{"op":"host","fn":"kv.set","args":{"key":"k","value":"v"}}`)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Errorf("granted host function: %d %s", rec.Code, rec.Body.String())
		}
		rec = call(`{"op":"host","fn":"entities.list","args":{}}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("a host function the plugin was not granted must not run: %d %s", rec.Code, rec.Body.String())
		}
	})

	// ---- a theme: install, preview (signed cookie, uncached), activate
	themePkg, err := packages.PackFiles(map[string][]byte{
		packages.ThemeManifest: []byte(`{"name":"sunrise","version":"1.0.0","settings_schema":{"type":"object","properties":{"word":{"type":"string","default":"morning"}}}}`),
		"partials/footer.html": []byte(`{{define "footer"}}<footer id="sunrise">{{.Settings.word}}</footer>{{end}}`),
		"assets/css/base.css":  []byte(`body{background:#ffd}`),
	}, &key)
	if err != nil {
		t.Fatal(err)
	}
	if rec := upload(themePkg); rec.Code != 303 {
		t.Fatalf("theme upload: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(get("/nope-nope", false).Body.String(), `id="sunrise"`) {
		t.Fatal("installing a theme must not activate it")
	}

	var cookie *http.Cookie
	t.Run("preview", func(t *testing.T) {
		if rec := get("/admin/preview/theme/sunrise", false); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Fatalf("anonymous preview: %d %s", rec.Code, rec.Header().Get("Location"))
		}
		rec := get("/admin/preview/theme/sunrise", true)
		loc := rec.Header().Get("Location")
		if rec.Code != 303 || !strings.HasPrefix(loc, "/?hm_theme_preview=") {
			t.Fatalf("preview link: %d %s", rec.Code, loc)
		}
		start := get(loc, false)
		if start.Code != 303 || start.Header().Get("Location") != "/" || len(start.Result().Cookies()) != 1 {
			t.Fatalf("preview start: %d %v", start.Code, start.Header())
		}
		cookie = start.Result().Cookies()[0]
		page := get("/nope-nope", false, cookie)
		if !strings.Contains(page.Body.String(), `<footer id="sunrise">morning</footer>`) {
			t.Fatalf("preview did not render the theme:\n%s", page.Body.String())
		}
		if cc := page.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") && !strings.Contains(cc, "private") {
			t.Errorf("a preview must never be shared-cached: %q", cc)
		}
		if strings.Contains(get("/nope-nope", false).Body.String(), `id="sunrise"`) {
			t.Error("the preview leaked to a visitor without the cookie")
		}
		cssHref := regexp.MustCompile(`/theme/assets/theme\.[0-9a-f]+\.css`).FindString(page.Body.String())
		if get(cssHref, false, cookie).Code != 200 {
			t.Errorf("the preview theme's stylesheet must load for the previewer: %s", cssHref)
		}
		forged := &http.Cookie{Name: previewCookie, Value: "c3Vuc3Jpc2V8OTk5OTk5OTk5OQ.forged"}
		if strings.Contains(get("/nope-nope", false, forged).Body.String(), `id="sunrise"`) {
			t.Error("a forged preview cookie must not work")
		}
	})

	t.Run("activate and settings", func(t *testing.T) {
		if rec := get("/admin/theme/themes/sunrise/activate", true); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Activate sunrise?") {
			t.Fatalf("activate page: %d", rec.Code)
		}
		if rec := postForm("/admin/theme/themes/sunrise/activate", url.Values{}); rec.Code != 303 {
			t.Fatalf("activate: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(get("/nope-nope", false).Body.String(), `<footer id="sunrise">morning</footer>`) {
			t.Fatal("the active theme must serve every visitor")
		}
		if rec := postForm("/admin/theme/themes/sunrise/settings", url.Values{"word": {"dusk"}}); rec.Code != 303 {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(get("/nope-nope", false).Body.String(), `<footer id="sunrise">dusk</footer>`) {
			t.Fatal("theme settings must reach templates as .Settings at once")
		}
		if rec := postForm("/admin/theme/themes/sunrise/uninstall", url.Values{}); rec.Code != http.StatusConflict {
			t.Errorf("the active theme cannot be uninstalled: %d", rec.Code)
		}
	})

	t.Run("hostile uploads are refused", func(t *testing.T) {
		other, _ := packages.GenerateKey()
		wrongSigner, _ := packages.PackFiles(map[string][]byte{packages.ThemeManifest: []byte(`{"name":"evil","version":"1.0.0"}`), "layouts/base.html": []byte("x")}, &other)
		if rec := upload(wrongSigner); rec.Code != 422 || !strings.Contains(rec.Body.String(), "do not trust") {
			t.Errorf("wrong signer: %d %s", rec.Code, rec.Body.String())
		}
		unsigned, _ := packages.PackFiles(map[string][]byte{packages.ThemeManifest: []byte(`{"name":"evil","version":"1.0.0"}`), "layouts/base.html": []byte("x")}, nil)
		if rec := upload(unsigned); rec.Code != 422 || !strings.Contains(rec.Body.String(), "not signed") {
			t.Errorf("unsigned: %d %s", rec.Code, rec.Body.String())
		}
		if rec := upload([]byte("PK\x03\x04 nonsense")); rec.Code != 422 {
			t.Errorf("garbage: %d", rec.Code)
		}
	})

	// ---- uninstall, with the data dropped
	if rec := postForm("/admin/theme/themes/default/activate", url.Values{}); rec.Code != 303 {
		t.Fatalf("back to the built-in theme: %d", rec.Code)
	}
	if strings.Contains(get("/nope-nope", false).Body.String(), `id="sunrise"`) {
		t.Fatal("the built-in theme is back")
	}
	if rec := postForm("/admin/plugins/capa/uninstall", url.Values{"drop_data": {"1"}}); rec.Code != 303 {
		t.Fatalf("uninstall: %d %s", rec.Code, rec.Body.String())
	}
	if get("/p/capa/assets/plugin.css", false).Code != 404 || strings.Contains(get("/nope-nope", false).Body.String(), "/p/capa/") {
		t.Error("an uninstalled plugin leaves nothing live")
	}
	var schema bool
	_ = db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, pluginhost.SchemaName("capa")).Scan(&schema)
	if schema {
		t.Error("the plugin's schema survived a data drop")
	}
}
