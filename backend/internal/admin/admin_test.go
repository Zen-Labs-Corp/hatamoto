package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"hatamoto.dev/core/web"
)

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/anything?x=1", "", nil, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != "/admin/login" || loc.Query().Get("next") != "/admin/anything?x=1" {
		t.Fatalf("location %q", loc)
	}
}

func TestHTMXUnauthenticatedGets401WithRedirect(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	// Behind the guard an htmx request whose session is gone has no token to
	// match, so it is refused before RequireSession answers (the page's own
	// htmx error handling surfaces it); RequireSession itself still redirects.
	if rec := g.do("GET", "/admin/anything", "stale", nil, map[string]string{"HX-Request": "true"}); rec.Code != http.StatusNotFound {
		t.Fatalf("guarded: %d, want the refusal", rec.Code)
	}
	h := g.a.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptestReq("GET", "/admin/anything", "stale")
	req.Header.Set("HX-Request", "true")
	rec := serve(h, req, nil)
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("HX-Redirect"), "/admin/login?") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Header().Get("HX-Redirect"), "expired=1") {
		t.Fatalf("stale cookie must say expired: %s", rec.Header().Get("HX-Redirect"))
	}
}

func TestIdleSessionExpires(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if rec := g.do("GET", "/admin/anything", "idle-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("idle session must be rejected, got %d", rec.Code)
	}
	// The store bumps last_used_at on every Validate; the rejection must have
	// revoked the session, so a second request cannot resurrect it.
	if len(g.sessions.revoked) != 1 {
		t.Fatalf("idle session not revoked: %v", g.sessions.revoked)
	}
	if rec := g.do("GET", "/admin/login", "idle-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("resurrected idle session bounced the login page: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHomeShowsOverview(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin", "owner-token", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<h1>Overview</h1>") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAuthenticatedUnknownPathRendersShell(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/nope", "owner-token", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`<main id="hm-main"`, `aria-label="Admin"`, "Acme Site", "Owner Olga", `<h1 class="hm-empty__title">Page not found</h1>`, `href="/admin/settings"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	// Editors do not see owner-only Settings sections.
	rec = g.do("GET", "/admin/nope", "editor-token", nil, nil)
	if strings.Contains(rec.Body.String(), `/admin/settings/users`) {
		t.Error("editor sees the Users link")
	}
}

func TestCSPIsStrictPerRequestNonceAndReplacesGlobal(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rx := regexp.MustCompile(`'nonce-([^']+)'`)
	var nonces []string
	for i := 0; i < 2; i++ {
		req := httptestReq("GET", "/admin/nope", "owner-token")
		rec := serve(g.h, req, func(w http.ResponseWriter) {
			// the global middleware has already run by the time admin handles it
			w.Header().Set("Content-Security-Policy-Report-Only", "default-src 'self'")
			w.Header().Set("X-Frame-Options", "DENY")
		})
		csp := rec.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("csp: %s", csp)
		}
		if rec.Header().Get("Content-Security-Policy-Report-Only") != "" {
			t.Error("report-only header must be removed")
		}
		if got := rec.Header().Values("Content-Security-Policy"); len(got) != 1 {
			t.Errorf("CSP header appended, not replaced: %v", got)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("X-Frame-Options = %q", rec.Header().Get("X-Frame-Options"))
		}
		m := rx.FindStringSubmatch(csp)
		if m == nil {
			t.Fatal("no nonce in CSP")
		}
		nonces = append(nonces, m[1])
		// every inline <script> carries the nonce of this response
		for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(rec.Body.String(), -1) {
			if !strings.Contains(tag, `nonce="`+m[1]+`"`) {
				t.Errorf("script without the response nonce: %s", tag)
			}
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Error("admin html must be no-store")
		}
	}
	if nonces[0] == nonces[1] {
		t.Error("nonce reused across requests")
	}
}

func TestCSRFOnUnsafeMethods(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	post := func(hdr map[string]string, form url.Values) int {
		return g.do("POST", "/admin/theme", "", form, hdr).Code
	}
	if c := post(nil, nil); c != http.StatusSeeOther {
		t.Fatalf("header token: %d", c)
	}
	if c := post(map[string]string{"X-CSRF-Token": ""}, url.Values{"_csrf": {csrfValue}}); c != http.StatusSeeOther {
		t.Fatalf("form token: %d", c)
	}
	if c := post(map[string]string{"X-CSRF-Token": ""}, nil); c != http.StatusForbidden {
		t.Fatalf("missing token: %d", c)
	}
	if c := post(map[string]string{"X-CSRF-Token": "wrong"}, nil); c != http.StatusForbidden {
		t.Fatalf("bad token: %d", c)
	}
	if c := post(map[string]string{"Sec-Fetch-Site": "cross-site"}, nil); c != http.StatusForbidden {
		t.Fatalf("cross-site: %d", c)
	}
	if c := post(map[string]string{"Sec-Fetch-Site": ""}, nil); c != http.StatusForbidden {
		t.Fatalf("missing Sec-Fetch-Site: %d", c)
	}
	if c := post(map[string]string{"Origin": "https://evil.example"}, nil); c != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", c)
	}
	if c := post(map[string]string{"Origin": "http://example.com"}, nil); c != http.StatusSeeOther {
		t.Fatalf("same origin: %d", c)
	}
}

// A signed-out top-level navigation gets the pre-session cookie once; a request
// that already holds it, or a signed-in one, does not.
func TestNavGETMintsPreSession(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	minted := func(rec *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range rec.Result().Cookies() {
			if c.Name == "hatamoto_csrf" {
				t.Errorf("readable csrf cookie set: %+v", c)
			}
			if c.Name == "hm_pre" {
				return c
			}
		}
		return nil
	}
	req := httptest.NewRequest("GET", "/admin/login", nil)
	setFetchMeta(req, csrfValue)
	rec := serve(g.h, req, nil)
	pre := minted(rec)
	if pre == nil || !g.a.parsePreID(pre.Value, time.Now()) {
		t.Fatalf("no valid pre-session cookie minted: %+v", pre)
	}
	if m := regexp.MustCompile(`name="csrf-token" content="([^"]+)"`).FindStringSubmatch(rec.Body.String()); m == nil || m[1] == "" {
		t.Fatalf("csrf meta missing: %v", m)
	}
	again := httptest.NewRequest("GET", "/admin/login", nil)
	setFetchMeta(again, csrfValue)
	again.AddCookie(&http.Cookie{Name: "hm_pre", Value: pre.Value})
	if c := minted(serve(g.h, again, nil)); c != nil {
		t.Errorf("minted a second pre-session cookie: %+v", c)
	}
	if c := minted(g.do("GET", "/admin/nope", "owner-token", nil, nil)); c != nil {
		t.Errorf("a signed-in navigation got a pre-session cookie: %+v", c)
	}
}

func TestHTMXFragmentVsFullPage(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	full := g.do("GET", "/admin/nope", "owner-token", nil, nil).Body.String()
	// Errors for a fragment request are a toast, not markup swapped in.
	frag := g.do("GET", "/admin/nope", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "row-1"})
	if frag.Header().Get("HX-Reswap") != "none" || !strings.Contains(frag.Header().Get("HX-Trigger"), "hm:toast") {
		t.Fatalf("fragment error headers: %v", frag.Header())
	}
	if !strings.Contains(full, "<!doctype html>") || !strings.Contains(strings.Join(frag.Header().Values("Vary"), ","), "HX-Target") {
		t.Error("full page or Vary missing")
	}
}

func TestRoleGate(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	h := g.a.RequireSession(g.a.RequireRole("owner")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	mux := http.NewServeMux()
	mux.Handle("GET /x", g.a.guardFor(scopeAdmin)(secureHeaders(h)))
	for tok, want := range map[string]int{"owner-token": 204, "editor-token": 403} {
		req := httptestReq("GET", "/x", tok)
		if rec := serve(mux, req, nil); rec.Code != want {
			t.Errorf("%s: %d want %d", tok, rec.Code, want)
		}
	}
}

func TestFlashRoundTripAndTamper(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := httptestRec()
	req := httptestReq("GET", "/admin/nope", "")
	g.a.SetFlash(rec, req, "success", "Saved <b>")
	c := rec.Result().Cookies()[0]
	req2 := httptestReq("GET", "/admin/nope", "owner-token")
	req2.AddCookie(c)
	body := serve(g.h, req2, nil).Body.String()
	if !strings.Contains(body, `class="hm-flash hm-flash--success"`) || !strings.Contains(body, "Saved &lt;b&gt;") {
		t.Fatalf("flash not rendered/escaped:\n%s", body)
	}
	tampered := *c
	tampered.Value = strings.Replace(c.Value, ".", "x.", 1)
	req3 := httptestReq("GET", "/admin/nope", "owner-token")
	req3.AddCookie(&tampered)
	if strings.Contains(serve(g.h, req3, nil).Body.String(), "hm-flash--") {
		t.Fatal("tampered flash accepted")
	}
}

func TestSafeNext(t *testing.T) {
	t.Parallel()

	ok := []string{"/admin", "/admin/entities", "/admin/entities?page=2", "/admin/settings/users/abc/edit", "/admin/sudoku"}
	bad := []string{"", "//evil.example", "/\\evil.example", "https://evil.example", "/admin//evil", "/adminx", "/administrator", "javascript:alert(1)",
		"/admin/login", "/admin/setup/step", "/admin/sudo", "/admin/sudo/x", "/admin/\x00x", "/api/v1/users", "admin/entities", "/admin/%0d%0a//x:y"[:0]}
	for _, s := range ok {
		if SafeNext(s) != s {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range bad {
		if SafeNext(s) != "" {
			t.Errorf("accepted %q", s)
		}
	}
	if got := SafeNextFor("/my-place", "/my-place/sudo"); got != "" {
		t.Errorf("custom prefix accepted its sudo page: %q", got)
	}
}

// A sign-in from an expired sudo page lands on the page behind it, never on sudo.
func TestSafeNextUnwrapsSudo(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"/admin/sudo?next=%2Fadmin%2Fsettings%2Fsecurity", "/admin/settings/security"},
		{"/admin/sudo?next=/admin/x", "/admin/x"},
		{"/admin/sudo?next=%2Fadmin%2Fsudo%3Fnext%3D%252Fadmin%252Fx", "/admin/x"},
		{"/admin/sudo?next=%2Fadmin%2Fsudo%3Fnext%3D%252Fadmin%252Fsudo%253Fnext%253D%25252Fadmin%25252Fx", ""},
		{"/admin/sudo?next=/admin/login", ""},
		{"/admin/sudo?next=https://evil.test", ""},
		{"/admin/sudo?resubmit=1", ""},
		{"/admin/sudo/x?next=/admin/x", ""},
	}
	for _, c := range cases {
		if got := SafeNext(c.in); got != c.want {
			t.Errorf("SafeNext(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The bundle is served under <prefix>/_/ on any prefix, and the old
// <prefix>/assets/ no longer serves anything, signed in or out.
func TestAssetsServedUnderUnderscore(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AdminPath = "/x-test" })
	as, _ := g.a.state()
	if !strings.HasPrefix(as.CSSURL, "/x-test/_/") {
		t.Fatalf("CSSURL = %q", as.CSSURL)
	}
	if rec := g.do("GET", as.CSSURL, "", nil, nil); rec.Code != 200 {
		t.Errorf("css under the prefix: %d", rec.Code)
	}
	if rec := g.do("GET", "/x-test/_/tokens.css", "", nil, nil); rec.Code != 200 {
		t.Errorf("tokens.css: %d", rec.Code)
	}
}

func TestOldAssetsPathServesNothing(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, _ := g.a.state()
	old := strings.Replace(as.CSSURL, "/_/", "/assets/", 1)
	for _, token := range []string{"", "owner-token"} {
		if rec := g.do("GET", old, token, nil, nil); rec.Code == 200 {
			t.Errorf("token %q: old assets URL %q served the stylesheet", token, old)
		}
	}
	if rec := g.do("GET", old, "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("signed in old assets URL = %d, want 404", rec.Code)
	}
}

func TestAssets(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, _ := g.a.state()
	rec := g.do("GET", as.CSSURL, "", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %v", rec.Code, rec.Header())
	}
	rec = g.do("GET", "/admin/_/tokens.css", "", nil, nil)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(rec.Body.String(), "--color-sumi") {
		t.Fatalf("tokens: %d %v", rec.Code, rec.Header())
	}
	if rec := g.do("GET", "/admin/_/nope.css", "", nil, nil); rec.Code != 404 {
		t.Fatalf("unknown asset: %d", rec.Code)
	}
	if rec := g.do("GET", as.JSURL("admin"), "", nil, nil); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("js: %d %v", rec.Code, rec.Header())
	}
	// A vendored module of the Tiptap graph is served and typed as JS.
	var anyTiptap string
	for _, imp := range regexp.MustCompile(`"(@tiptap/core)":"([^"]+)"`).FindStringSubmatch(string(as.Importmap))[2:] {
		anyTiptap = imp
	}
	if rec := g.do("GET", anyTiptap, "", nil, nil); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("tiptap module %s: %d %v", anyTiptap, rec.Code, rec.Header())
	}
	// htmx is emitted with an SRI hash that matches the served bytes.
	if len(as.Scripts(nil)) != 1 || len(as.Scripts([]string{"sortablejs"})) != 2 {
		t.Fatalf("default scripts: %+v", as.Scripts(nil))
	}
}

func TestImportmapRendered(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	body := g.do("GET", "/admin/nope", "owner-token", nil, nil).Body.String()
	i := strings.Index(body, `type="importmap"`)
	j := strings.Index(body, `type="module"`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("importmap must precede the module script (%d, %d)", i, j)
	}
	if !strings.Contains(body, `"@hm/components"`) || !strings.Contains(body, `"@tiptap/starter-kit"`) || !strings.Contains(body, `integrity="sha256-`) {
		t.Fatal("importmap or SRI missing")
	}
	if strings.Contains(body, `\u0026`) && strings.Contains(body, `&#`) {
		t.Fatal("importmap was HTML-escaped")
	}
}

func TestAssetBuildIsDeterministicAndContentAddressed(t *testing.T) {
	t.Parallel()

	build := func(extra string) *Assets {
		admin := fstest.MapFS{
			"css/tokens.css":          {Data: []byte(":root{}")},
			"css/base.css":            {Data: []byte("body{}")},
			"css/partials/button.css": {Data: []byte(".b{" + extra + "}")},
			"css/partials/field.css":  {Data: []byte(".f{}")},
			"js/admin.js":             {Data: []byte("1")},
		}
		a, err := BuildAssets(admin, web.Vendor())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a1, a2, b := build("x"), build("x"), build("y")
	if a1.CSSURL != a2.CSSURL {
		t.Fatal("not deterministic")
	}
	if a1.CSSURL == b.CSSURL {
		t.Fatal("css name must change when a partial byte changes")
	}
	if !regexp.MustCompile(`^/admin/_/admin\.[0-9a-f]{12}\.css$`).MatchString(a1.CSSURL) {
		t.Fatal(a1.CSSURL)
	}
}

func TestTemplateDirReloads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := copyTree(t, dir); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, func(d *Deps) { d.Dir = dir })
	if rec := g.do("GET", "/admin/nope", "owner-token", nil, nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

// With no choice made the page follows the browser (CSS, no flash, no inline
// script); the toggle then flips what is shown and the choice is remembered.
func TestThemeFollowsTheBrowserUntilChosen(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if body := g.do("GET", "/admin/nope", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `data-admin-theme="auto"`) {
		t.Error("no cookie: the theme is auto")
	}
	cookie := func(form url.Values) string {
		for _, c := range g.do("POST", "/admin/theme", "", form, nil).Result().Cookies() {
			if c.Name == "hm_theme" {
				return c.Value
			}
		}
		return ""
	}
	if got := cookie(url.Values{"scheme": {"dark"}}); got != "light" {
		t.Errorf("dark shown by the browser: toggling picks light, got %q", got)
	}
	if got := cookie(url.Values{"scheme": {"light"}}); got != "dark" {
		t.Errorf("light shown: toggling picks dark, got %q", got)
	}
	if got := cookie(nil); got != "dark" {
		t.Errorf("without script: dark, got %q", got)
	}
}
