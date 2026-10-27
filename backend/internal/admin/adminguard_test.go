package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/httpx"
)

func TestGuardNavigationOnly(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, c := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"navigate+document", nil, 200},
		{"cors+empty", map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, 404},
		{"navigate+iframe", map[string]string{"Sec-Fetch-Dest": "iframe"}, 404},
		{"same-origin cors+empty", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": ""}, 404},
		{"no metadata at all", map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": "", "Sec-Fetch-Dest": ""}, 404},
		{"navigate+empty", map[string]string{"Sec-Fetch-Dest": ""}, 404},
		{"no-cors+document", map[string]string{"Sec-Fetch-Mode": "no-cors"}, 404},
	} {
		if rec := g.do("GET", "/admin/profile", "owner-token", nil, c.hdr); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestGuardPrefetchAllowed(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, purpose := range []string{"prefetch", "prerender"} {
		rec := g.do("GET", "/admin/profile", "owner-token", nil, map[string]string{"Sec-Purpose": purpose})
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", purpose, rec.Code, rec.Header())
		}
	}
}

func TestGuardIgnoresHXRequest(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", "/admin/profile", "owner-token", nil, map[string]string{"HX-Request": "true", "X-CSRF-Token": "", "Sec-Fetch-Mode": "cors"})
	if rec.Code != 404 {
		t.Errorf("HX-Request without a token: %d, want 404", rec.Code)
	}
}

func TestGuardHeaders(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, c := range []struct {
		name, path, token string
		hdr               map[string]string
		code              int
		document          bool
	}{
		{"page", "/admin/profile", "owner-token", nil, 200, true},
		{"unknown path", "/admin/nope", "owner-token", nil, 404, true},
		{"redirect", "/admin/profile", "", nil, 303, false},
		{"htmx partial", "/admin/profile", "owner-token", map[string]string{"HX-Request": "true"}, 200, false},
		// The refusal body is the site's standard 404, which carries the server's
		// security headers, not the admin CSP; the sealed headers still apply.
		{"refusal", "/admin/profile", "owner-token", map[string]string{"Sec-Fetch-Dest": "iframe"}, 404, false},
	} {
		rec := g.do("GET", c.path, c.token, nil, c.hdr)
		h := rec.Header()
		if rec.Code != c.code {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.code)
		}
		if h.Get("Cache-Control") != "no-store" || h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers %v", c.name, h)
		}
		if c.document && (!strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("X-Frame-Options") != "DENY") {
			t.Errorf("%s: frame headers %q %q", c.name, h.Get("Content-Security-Policy"), h.Get("X-Frame-Options"))
		}
	}
}

// A handler's own Cache-Control cannot weaken the seal.
func TestSealWriterForcesHeaders(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	h := g.a.guardFor(scopeAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := serve(h, httptestReq("GET", "/admin/anything", "owner-token"), nil)
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestGuardAssetsKeepCaching(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, _ := g.a.state()
	fetch := map[string]string{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "style"}
	rec := g.do("GET", as.CSSURL, "", nil, fetch)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("hashed css: %d %v", rec.Code, rec.Header())
	}
	rec = g.do("GET", "/admin/_/tokens.css", "", nil, map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=300") {
		t.Errorf("tokens.css: %d %v", rec.Code, rec.Header())
	}
	if h := rec.Header(); h.Get("Cross-Origin-Opener-Policy") != "" || h.Get("Cache-Control") == "no-store" {
		t.Errorf("assets are not sealed: %v", h)
	}
}

func TestGuardFrameTickets(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.a.deps.Content.Preview = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("preview")) })
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g.a.tickets.now = clock.now
	owner, editor := g.sessions.byToken["owner-token"].ID, g.sessions.byToken["editor-token"].ID
	id := uuid.New()
	frame := map[string]string{"Sec-Fetch-Dest": "iframe"}
	pathFor := func(e uuid.UUID, tok string) string { return "/admin/entities/" + e.String() + "/edit/preview/" + tok }
	mint := func() string { return g.a.tickets.mint(ticketPreview, owner, id.String(), []byte("{}")) }

	want := g.do("GET", "/admin/nothing-here", "owner-token", nil, frame)
	if want.Code != 404 {
		t.Fatalf("precondition: unknown admin URL %d", want.Code)
	}
	refused := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != 404 || stripNonce(rec.Body.String()) != stripNonce(want.Body.String()) {
			t.Errorf("%s: %d, want the admin refusal", name, rec.Code)
		}
	}

	// A valid ticket loads once, with the render handler's framing headers.
	tok := mint()
	rec := g.do("GET", pathFor(id, tok), "owner-token", nil, frame)
	h := rec.Header()
	if rec.Code != 200 || rec.Body.String() != "preview" {
		t.Fatalf("preview frame: %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'self'") || h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("preview must stay frameable: %v", h)
	}
	if h.Get("Cross-Origin-Opener-Policy") != "" {
		t.Errorf("frame response sealed with COOP: %v", h)
	}
	refused("reused", g.do("GET", pathFor(id, tok), "owner-token", nil, frame))

	refused("garbage", g.do("GET", pathFor(id, strings.Repeat("A", 32)), "owner-token", nil, frame))
	refused("short", g.do("GET", pathFor(id, "abc"), "owner-token", nil, frame))
	refused("no session", g.do("GET", pathFor(id, mint()), "", nil, frame))
	refused("other session", g.do("GET", pathFor(id, mint()), "editor-token", nil, frame))
	refused("other entity", g.do("GET", pathFor(uuid.New(), mint()), "owner-token", nil, frame))
	refused("wrong purpose", g.do("GET", pathFor(id, g.a.tickets.mint(ticketPluginUI, owner, id.String(), nil)), "owner-token", nil, frame))
	refused("minted for the other session", g.do("GET", pathFor(id, g.a.tickets.mint(ticketPreview, editor, id.String(), nil)), "owner-token", nil, frame))
	tok = mint()
	clock.t = clock.t.Add(ticketTTL + time.Second)
	refused("expired", g.do("GET", pathFor(id, tok), "owner-token", nil, frame))

	for name, hdr := range map[string]map[string]string{
		"cross-site":     {"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "cross-site"},
		"fetch":          {"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"},
		"wrong dest":     {"Sec-Fetch-Dest": "object"},
		"no metadata":    {"Sec-Fetch-Site": "", "Sec-Fetch-Mode": "", "Sec-Fetch-Dest": ""},
		"navigate+frame": {"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "iframe"},
	} {
		if rec := g.do("GET", pathFor(id, mint()), "owner-token", nil, hdr); rec.Code != 404 {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	// Only the preview URL is framable.
	if rec := g.do("GET", "/admin/profile", "owner-token", nil, frame); rec.Code != 404 {
		t.Errorf("other admin URL as iframe: %d, want 404", rec.Code)
	}
}

func TestPluginUIGuard(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, c := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"iframe", map[string]string{"Sec-Fetch-Dest": "iframe"}, 200},
		{"iframe from a sandboxed opaque origin", map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "cross-site"}, 404},
		{"script", map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors"}, 200},
		{"style", map[string]string{"Sec-Fetch-Dest": "style"}, 200},
		{"image", map[string]string{"Sec-Fetch-Dest": "image"}, 200},
		{"font", map[string]string{"Sec-Fetch-Dest": "font"}, 200},
		{"document", nil, 404},
		{"fetch", map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, 404},
		{"no metadata", map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": "", "Sec-Fetch-Dest": ""}, 404},
	} {
		if rec := g.do("GET", pluginDoc(g, "owner-token", "hello", "index.html"), "owner-token", nil, c.hdr); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestGuardServiceWorkerRefused(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, c := range []struct {
		path string
		hdr  map[string]string
	}{
		{"/admin/profile", nil},
		{"/admin/_/tokens.css", nil},
		{"plugin-ui", map[string]string{"Sec-Fetch-Dest": "iframe"}},
		{"/auth/login", nil},
		{"/-/edit?id=" + uuid.NewString(), map[string]string{"Sec-Fetch-User": "?1"}},
	} {
		path := func() string { // a plugin document needs a fresh one-time ticket per request
			if c.path == "plugin-ui" {
				return pluginDoc(g, "owner-token", "hello", "index.html")
			}
			return c.path
		}
		if rec := g.do("GET", path(), "owner-token", nil, c.hdr); rec.Code == 404 {
			t.Fatalf("%s: precondition, 404 without Service-Worker", c.path)
		}
		hdr := map[string]string{"Service-Worker": "script"}
		for k, v := range c.hdr {
			hdr[k] = v
		}
		if rec := g.do("GET", path(), "owner-token", nil, hdr); rec.Code != 404 {
			t.Errorf("%s with Service-Worker: %d, want 404", c.path, rec.Code)
		}
	}
}

// A refusal is whatever the server registered as the standard not-found
// responder, with the sealed headers, and without the admin's page chrome.
// It swaps the process-wide refuser, so it must not run in parallel.
func TestRefusalIsTheRegisteredRefuser(t *testing.T) {
	const marker = "marker-refuser-body"
	httpx.RegisterRefuser(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(marker))
	})
	t.Cleanup(func() { httpx.RegisterRefuser(http.NotFound) })

	g := newRig(t)
	g.realTokens()
	for _, hx := range []bool{false, true} {
		hdr := map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""}
		if hx {
			hdr["HX-Request"] = "true"
		}
		rec := g.do("GET", "/admin/profile", "owner-token", nil, hdr)
		if rec.Code != http.StatusNotFound || rec.Body.String() != marker {
			t.Errorf("htmx=%v: %d %q, want 404 and the registered refuser's body", hx, rec.Code, rec.Body.String())
		}
		if (rec.Header().Get("HX-Refresh") == "true") != hx {
			t.Errorf("htmx=%v: HX-Refresh %q", hx, rec.Header().Get("HX-Refresh"))
		}
		if h := rec.Header(); h.Get("Cache-Control") != "no-store" || h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("htmx=%v: refusal headers %v", hx, h)
		}
		if rec.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("htmx=%v: the refusal carries the admin CSP", hx)
		}
	}
}

// A valid token and session are not enough: the browser must also say the
// request is same-origin (a missing header is how a non-browser or an old
// browser looks, and neither may drive an unsafe admin request).
func TestGuardMissingFetchSiteRefused(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for _, site := range []string{"", "none", "same-site", "cross-site"} {
		if rec := g.do("POST", "/admin/theme", "owner-token", nil, map[string]string{"Sec-Fetch-Site": site}); rec.Code != http.StatusForbidden {
			t.Errorf("signed in, Sec-Fetch-Site %q: %d, want 403", site, rec.Code)
		}
		// Pre-session form: the sign-in page posts without a session.
		form := url.Values{"email": {"owner@example.com"}, "password": {"x"}}
		if rec := g.do("POST", "/admin/login", "", form, map[string]string{"Sec-Fetch-Site": site}); rec.Code != http.StatusForbidden {
			t.Errorf("login, Sec-Fetch-Site %q: %d, want 403", site, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/theme", "owner-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin control: %d", rec.Code)
	}
}

// ---- the synchronizer token (real derivation) ----

func TestGuardTokenOnNonNavigation(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	htmx := func(token string) int {
		hdr := map[string]string{"HX-Request": "true"}
		if token != "-" {
			hdr["X-CSRF-Token"] = token
		}
		return g.do("GET", "/admin/profile", "owner-token", nil, hdr).Code
	}
	if c := htmx(""); c != 404 {
		t.Errorf("no token: %d", c)
	}
	if c := htmx("wrong"); c != 404 {
		t.Errorf("wrong token: %d", c)
	}
	if c := htmx(g.tokenFor("editor-token")); c != 404 {
		t.Errorf("another session's token: %d", c)
	}
	if c := htmx(g.tokenFor("owner-token")); c != 200 {
		t.Errorf("right token: %d", c)
	}
	// The token of a revoked session is worth nothing, even with a cookie still sent.
	tok := g.tokenFor("owner-token")
	_ = g.sessions.Revoke(context.Background(), g.sessions.byToken["owner-token"].ID)
	rec := g.do("GET", "/admin/profile", "owner-token", nil, map[string]string{"HX-Request": "true", "X-CSRF-Token": tok})
	if rec.Code != 404 {
		t.Errorf("revoked session's token: %d", rec.Code)
	}
}

func TestTokenReplayAcrossSessions(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	owner := g.tokenFor("owner-token")
	if rec := g.do("GET", "/admin/profile", "editor-token", nil, map[string]string{"HX-Request": "true", "X-CSRF-Token": owner}); rec.Code != 404 {
		t.Errorf("GET with the owner's token on the editor's session: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme", "editor-token", nil, map[string]string{"X-CSRF-Token": owner}); rec.Code != http.StatusForbidden {
		t.Errorf("POST with the owner's token on the editor's session: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme", "editor-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("POST with the editor's own token: %d", rec.Code)
	}
}

func TestGuardUnsafeNeedsToken(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	tok := g.tokenFor("owner-token")
	form := func(token string) map[string]string { return map[string]string{"X-CSRF-Token": token} }

	if c := g.do("POST", "/admin/theme", "owner-token", nil, form(tok)).Code; c != 303 {
		t.Errorf("header token: %d", c)
	}
	if c := g.do("POST", "/admin/theme", "owner-token", url.Values{"_csrf": {tok}}, form("")).Code; c != 303 {
		t.Errorf("_csrf field: %d", c)
	}
	for name, hdr := range map[string]map[string]string{"none": form(""), "wrong": form("wrong")} {
		if c := g.do("POST", "/admin/theme", "owner-token", nil, hdr).Code; c != 403 {
			t.Errorf("%s token: %d", name, c)
		}
	}
	if c := g.do("POST", "/admin/theme?_csrf="+url.QueryEscape(tok), "owner-token", nil, form("")).Code; c != 403 {
		t.Errorf("token in the query string: %d", c)
	}

	multi := func(fields ...[2]string) int {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for _, f := range fields {
			_ = mw.WriteField(f[0], f[1])
		}
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/admin/theme", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
		g.addFetchMeta(req, "owner-token", form(""))
		rec := httptest.NewRecorder()
		g.h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := multi([2]string{"_csrf", tok}, [2]string{"scheme", "dark"}); c != 303 {
		t.Errorf("multipart first-part token: %d", c)
	}
	if c := multi([2]string{"scheme", "dark"}, [2]string{"_csrf", tok}); c != 403 {
		t.Errorf("multipart token as a later part: %d", c)
	}
}

func TestNoReadableCSRFCookie(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	check := func(what string, rec *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range rec.Result().Cookies() {
			if c.Name == "hatamoto_csrf" {
				t.Errorf("%s sets the readable csrf cookie: %+v", what, c)
			}
		}
	}
	check("login GET", g.do("GET", "/admin/login", "", nil, nil))
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	rec := g.do("POST", "/admin/login", "", form, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST: %d", rec.Code)
	}
	check("login POST", rec)
	check("dashboard GET", g.do("GET", "/admin/nope", "owner-token", nil, nil))
}

func TestPreSessionCookieAttrs(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	req := httptest.NewRequest("GET", "/admin/login", nil)
	setFetchMeta(req, csrfValue)
	var pre *http.Cookie
	for _, c := range serve(g.h, req, nil).Result().Cookies() {
		if c.Name == "hm_pre" {
			pre = c
		}
	}
	if pre == nil || !pre.HttpOnly || pre.SameSite != http.SameSiteStrictMode || pre.Path != "/admin" || pre.MaxAge != int(preTTL/time.Second) {
		t.Fatalf("pre-session cookie: %+v", pre)
	}
}

func TestPreSessionFormsPost(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	login := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	if c := g.do("POST", "/admin/login", "", login, nil).Code; c != http.StatusSeeOther {
		t.Errorf("login: %d", c)
	}
	// The other public forms are not blocked (whatever their own validation says).
	for _, path := range []string{"/admin/forgot", "/admin/reset", "/admin/accept", "/admin/setup"} {
		if c := g.do("POST", path, "", url.Values{"email": {"olga@example.com"}}, nil).Code; c == http.StatusForbidden {
			t.Errorf("%s with the pre-session token: blocked", path)
		}
	}
}

func TestPreSessionRequiresCookie(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	login := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	fresh := g.a.newPreID(time.Now().Add(time.Hour))
	other := g.a.newPreID(time.Now().Add(time.Hour))
	expired := g.a.newPreID(time.Now().Add(-time.Minute))
	for name, hdr := range map[string]map[string]string{
		"token without the cookie": {"Cookie": "", "X-CSRF-Token": g.a.preToken(fresh)},
		"cookie without the token": {"Cookie": "hm_pre=" + fresh, "X-CSRF-Token": ""},
		"token of another cookie":  {"Cookie": "hm_pre=" + fresh, "X-CSRF-Token": g.a.preToken(other)},
		"expired cookie":           {"Cookie": "hm_pre=" + expired, "X-CSRF-Token": g.a.preToken(expired)},
		"malformed cookie":         {"Cookie": "hm_pre=abc", "X-CSRF-Token": g.a.preToken("abc")},
	} {
		if c := g.do("POST", "/admin/login", "", login, hdr).Code; c != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, c)
		}
	}
	if c := g.do("POST", "/admin/login", "", login, map[string]string{"Cookie": "hm_pre=" + fresh, "X-CSRF-Token": g.a.preToken(fresh)}).Code; c != http.StatusSeeOther {
		t.Errorf("control: %d", c)
	}
}

func TestPreSessionClearedOnSignIn(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}, nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hm_pre" && c.MaxAge < 0 && c.Path == "/admin" {
			return
		}
	}
	t.Errorf("sign-in does not expire hm_pre: %v", rec.Result().Cookies())
}

// A tab that still holds a session can sign in again: the form carries the
// session token, which is what the guard expects while a session exists. A form
// rendered before that session existed (an external link opened the admin in a
// signed-in browser) carries the pre-session token, which the pre-session
// routes also accept from a valid hm_pre; no other route does.
func TestReLoginWhileSignedIn(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	withPre := map[string]string{
		"X-CSRF-Token": g.a.preToken(pre),
		"Cookie":       "hatamoto_session=owner-token; hm_pre=" + pre,
	}
	// Each sign-in revokes the session it replaces, so the refusals come first.
	if c := g.do("POST", "/admin/profile", "owner-token", url.Values{"name": {"Olga"}}, withPre).Code; c != http.StatusForbidden {
		t.Errorf("pre-session token on a non-pre route while signed in: %d, want 403", c)
	}
	// A pre-session token whose cookie the browser does not hold is worth nothing.
	noCookie := map[string]string{"X-CSRF-Token": g.a.preToken(pre)}
	if c := g.do("POST", "/admin/login", "owner-token", form, noCookie).Code; c != http.StatusForbidden {
		t.Errorf("pre-session token without its cookie while signed in: %d, want 403", c)
	}
	if c := g.do("POST", "/admin/login", "owner-token", form, withPre).Code; c != http.StatusSeeOther {
		t.Errorf("pre-session token from a valid hm_pre while signed in: %d, want 303", c)
	}
	if c := g.do("POST", "/admin/login", "editor-token", form, nil).Code; c != http.StatusSeeOther {
		t.Errorf("re-login with the session token: %d", c)
	}
}

func TestEditLink(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	nav := map[string]string{"Sec-Fetch-User": "?1"}
	with := func(k, v string) map[string]string { return map[string]string{"Sec-Fetch-User": "?1", k: v} }
	g := newRig(t)
	// The guard's own refusal (an admin document fetched as an iframe).
	unknown := g.do("GET", "/admin/profile", "owner-token", nil, map[string]string{"Sec-Fetch-Dest": "iframe"})
	if unknown.Code != 404 {
		t.Fatalf("precondition: guard refusal %d", unknown.Code)
	}

	ok := g.do("GET", "/-/edit?id="+id, "owner-token", nil, nav)
	if ok.Code != 303 || ok.Header().Get("Location") != "/admin/entities/"+id+"/edit" {
		t.Fatalf("ok: %d %q", ok.Code, ok.Header().Get("Location"))
	}
	if ok.Header().Get("Cache-Control") != "no-store" || ok.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("ok headers: %v", ok.Header())
	}

	for _, c := range []struct {
		name, method, id, token string
		hdr                     map[string]string
	}{
		{"no Sec-Fetch-User", "GET", id, "owner-token", nil},
		{"iframe", "GET", id, "owner-token", with("Sec-Fetch-Dest", "iframe")},
		{"cors", "GET", id, "owner-token", with("Sec-Fetch-Mode", "cors")},
		{"no session", "GET", id, "", nav},
		{"expired session", "GET", id, "expired-token", nav}, // the store no longer knows it,
		{"not a uuid", "GET", "42", "owner-token", nav},
		{"scheme-relative id", "GET", "//evil.example", "owner-token", nav},
		{"dot-dot id", "GET", "../", "owner-token", nav},
		{"HEAD", "HEAD", id, "owner-token", nav},
		{"service worker", "GET", id, "owner-token", with("Service-Worker", "script")},
	} {
		rec := g.do(c.method, "/-/edit?id="+url.QueryEscape(c.id), c.token, nil, c.hdr)
		if rec.Code != 404 || (c.method != "HEAD" && stripNonce(rec.Body.String()) != stripNonce(unknown.Body.String())) {
			t.Errorf("%s: %d, want the admin 404 (location %q)", c.name, rec.Code, rec.Header().Get("Location"))
		}
	}

	p := newRig(t, func(d *Deps) { d.AdminPath = "/x-test" })
	if rec := p.do("GET", "/-/edit?id="+id, "owner-token", nil, nav); rec.Code != 303 || rec.Header().Get("Location") != "/x-test/entities/"+id+"/edit" {
		t.Errorf("prefixed: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func stripNonce(s string) string {
	return cfgNonce.ReplaceAllString(nonceRx.ReplaceAllString(s, ""), "$1")
}

// A refusal page must never carry the token the request failed to present: the
// layout's csrf-token meta is empty and the real token appears nowhere.
func TestRefusalPagesDoNotLeakToken(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	preCookie := map[string]string{"Cookie": "hm_pre=" + pre}
	metaRx := regexp.MustCompile(`name="csrf-token" content="([^"]*)"`)
	fetch := map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""}
	merge := func(m map[string]string, kv ...string) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			out[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	for _, c := range []struct {
		name, method, session string
		hdr                   map[string]string
		secret                string
		code                  int
	}{
		{"signed-in tokenless fetch GET", "GET", "owner-token", fetch, g.tokenFor("owner-token"), 404},
		{"signed-in tokenless POST", "POST", "owner-token", map[string]string{"X-CSRF-Token": ""}, g.tokenFor("owner-token"), 403},
		{"signed-in wrong-token POST", "POST", "owner-token", map[string]string{"X-CSRF-Token": "wrong"}, g.tokenFor("owner-token"), 403},
		{"pre-session tokenless fetch GET", "GET", "", merge(fetch, "Cookie", "hm_pre="+pre), g.a.preToken(pre), 404},
		{"pre-session tokenless POST", "POST", "", merge(preCookie, "X-CSRF-Token", ""), g.a.preToken(pre), 403},
		{"pre-session wrong-token POST", "POST", "", merge(preCookie, "X-CSRF-Token", "wrong"), g.a.preToken(pre), 403},
	} {
		target := "/admin/profile"
		if c.method == "POST" {
			target = "/admin/theme"
		}
		rec := g.do(c.method, target, c.session, nil, c.hdr)
		if rec.Code != c.code {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.code)
		}
		body := rec.Body.String()
		if strings.Contains(body, c.secret) {
			t.Errorf("%s: the refusal body contains the real token", c.name)
		}
		if m := metaRx.FindStringSubmatch(body); m != nil && m[1] != "" {
			t.Errorf("%s: csrf-token meta is %q, want empty", c.name, m[1])
		}
	}
}

// Under Referrer-Policy no-referrer a browser sends Origin "null" on a
// same-origin form POST navigation; Sec-Fetch-Site and the token carry the proof.
func TestBrowserFormPostOriginNull(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	login := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	if c := g.browserForm("/admin/login", "", pre, login, nil).Code; c != http.StatusSeeOther {
		t.Errorf("login form, same-origin, Origin null: %d, want 303", c)
	}
	if c := g.browserForm("/admin/login", "", pre, login, map[string]string{"Sec-Fetch-Site": "cross-site"}).Code; c != http.StatusForbidden {
		t.Errorf("login form, cross-site, Origin null: %d, want 403", c)
	}
	o := newOwnerRig(t, true)
	o.realTokens()
	o.a.deps.People = newFakePeople()
	profile := url.Values{"name": {"Edie"}, "photo": {"/media/edie.jpg"}}
	if c := o.browserForm("/admin/profile", "editor-token", "", profile, nil).Code; c != http.StatusSeeOther {
		t.Errorf("profile form, same-origin, Origin null: %d, want 303", c)
	}
	if c := o.browserForm("/admin/profile", "editor-token", "", profile, map[string]string{"Sec-Fetch-Site": "cross-site"}).Code; c != http.StatusForbidden {
		t.Errorf("profile form, cross-site, Origin null: %d, want 403", c)
	}
}

// Only GET and HEAD under the assets prefix are assets; any other method is an
// admin request and needs the token, so a refusal there is never a signed-in shell.
func TestUnsafeMethodsOnAssetsPathAreAdminRequests(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		rec := g.do(method, "/admin/_/x", "owner-token", nil, map[string]string{"X-CSRF-Token": ""})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", method, rec.Code)
		}
		if b := rec.Body.String(); strings.Contains(b, "Owner Olga") || strings.Contains(b, "owner@example.com") {
			t.Errorf("%s: the body is a signed-in shell", method)
		}
	}
}

// A page script can set an hm_pre at a more specific Path, which browsers send
// first. The guard counts every hm_pre, so the real one still signs the user in.
func TestPreCookieShadowingDoesNotLockOut(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	hdr := map[string]string{
		"X-CSRF-Token": g.a.preToken(pre),
		"Cookie":       "hm_pre=bogus; hm_pre=" + pre,
	}
	if c := g.do("POST", "/admin/login", "", form, hdr).Code; c != http.StatusSeeOther {
		t.Errorf("login with a bogus hm_pre first and the valid one second: %d, want 303", c)
	}
	// Navigating with only bogus cookies mints a valid one; with a valid one among them it does not.
	nav := g.do("GET", "/admin/login", "", nil, map[string]string{"Cookie": "hm_pre=bogus"})
	minted := false
	for _, c := range nav.Result().Cookies() {
		minted = minted || c.Name == "hm_pre" && c.MaxAge > 0
	}
	if !minted {
		t.Error("no hm_pre minted for a request with only a bogus one")
	}
	nav = g.do("GET", "/admin/login", "", nil, map[string]string{"Cookie": "hm_pre=bogus; hm_pre=" + pre})
	for _, c := range nav.Result().Cookies() {
		if c.Name == "hm_pre" {
			t.Errorf("hm_pre minted although a valid one was sent: %v", c)
		}
	}
}

// The theme toggle returns to the page it was used on: the posted "back" (the
// referrer is never sent), else the Referer, else the admin home. Anything off
// the admin prefix is ignored.
func TestThemeReturnsToBackPage(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	for _, c := range []struct {
		name, back, referer, want string
	}{
		{"posted back", "/admin/settings/users?page=2", "", "/admin/settings/users?page=2"},
		{"back wins over referer", "/admin/profile", "https://acme.example/admin/menus", "/admin/profile"},
		{"referer without back", "", "https://acme.example/admin/menus", "/admin/menus"},
		{"external back", "https://evil.example/admin", "", "/admin"},
		{"scheme-relative back", "//evil.example/admin", "", "/admin"},
		{"foreign path back", "/elsewhere", "", "/admin"},
		{"unsafe back falls to referer", "//evil.example", "https://acme.example/admin/menus", "/admin/menus"},
		{"nothing", "", "", "/admin"},
	} {
		form := url.Values{"scheme": {"dark"}}
		if c.back != "" {
			form.Set("back", c.back)
		}
		rec := g.do("POST", "/admin/theme", "owner-token", form, map[string]string{"Referer": c.referer})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != c.want {
			t.Errorf("%s: %d to %q, want %q", c.name, rec.Code, rec.Header().Get("Location"), c.want)
		}
	}
}

// A script that planted an hm_pre at a narrower path and learned its token can
// POST a pre-session route with it while the owner is signed in. The response
// page must carry that pre-session token, never the session token.
func TestPreSessionFallbackDoesNotLeakSessionToken(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.realTokens()
	session := g.tokenFor("owner-token")
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/admin/forgot", url.Values{"email": {"x@y.example"}}},
		{"/admin/login", url.Values{"email": {"olga@example.com"}, "password": {"wrong-password-here"}}},
		{"/admin/reset", url.Values{"token": {"bad"}, "password": {"short"}}},
		{"/admin/accept", url.Values{"token": {"bad"}, "password": {"short"}}},
		{"/admin/setup", url.Values{"email": {"x@y.example"}}},
	} {
		rec := g.do("POST", c.path, "owner-token", c.form, map[string]string{
			"X-CSRF-Token": g.a.preToken(pre),
			"Cookie":       "hatamoto_session=owner-token; hm_pre=" + pre,
		})
		if rec.Code == http.StatusForbidden && c.path != "/admin/setup" {
			t.Errorf("%s: the pre-session token was refused (%d), the test does not exercise the fallback", c.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), session) {
			t.Errorf("%s (%d): the response body contains the session token", c.path, rec.Code)
		}
		if c.path != "/admin/login" {
			continue
		}
		// The page carries the pre-session token; resubmitting it signs in.
		m := regexp.MustCompile(`name="_csrf" value="([^"]*)"`).FindStringSubmatch(rec.Body.String())
		if m == nil || m[1] != g.a.preToken(pre) {
			t.Errorf("%s: rendered token %v, want the pre-session token", c.path, m)
			continue
		}
		again := g.do("POST", c.path, "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}, "_csrf": {m[1]}}, map[string]string{
			"X-CSRF-Token": "",
			"Cookie":       "hm_pre=" + pre,
		})
		if again.Code != http.StatusSeeOther {
			t.Errorf("%s: resubmitting the rendered token: %d, want 303", c.path, again.Code)
		}
	}
}

// The pre-session lifetime is bounded on both sides: an id whose expiry is
// further out than a minted one could be is not valid, so a planted pair cannot
// live forever; a minted cookie lasts preTTL (12h).
func TestPreSessionLifetimeBounded(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	now := time.Now()
	for _, c := range []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"minted", now.Add(preTTL), true},
		{"within skew", now.Add(preTTL + time.Minute), true},
		{"beyond skew", now.Add(preTTL + time.Hour), false},
		{"eternal", time.Unix(99999999999, 0), false},
		{"expired", now.Add(-time.Minute), false},
	} {
		if got := g.a.parsePreID(g.a.newPreID(c.expires), now); got != c.want {
			t.Errorf("%s: valid = %v, want %v", c.name, got, c.want)
		}
	}

	rec := g.do("GET", "/admin/login", "", nil, map[string]string{"Cookie": ""})
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hm_pre" {
			if c.MaxAge != int(preTTL/time.Second) {
				t.Errorf("hm_pre MaxAge = %d, want %d", c.MaxAge, int(preTTL/time.Second))
			}
			return
		}
	}
	t.Errorf("no hm_pre minted: %v", rec.Result().Cookies())
}

// A refused htmx request carries HX-Refresh so the page reloads; the response
// is otherwise the plain refusal and does not depend on the session.
func TestRefusalAsksHTMXToRefresh(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	fetch := map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""}
	merge := func(extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range fetch {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	plain := g.do("GET", "/admin/profile", "owner-token", nil, fetch)
	if plain.Code != 404 || plain.Header().Get("HX-Refresh") != "" {
		t.Fatalf("plain refusal: %d, HX-Refresh %q", plain.Code, plain.Header().Get("HX-Refresh"))
	}
	for _, session := range []string{"owner-token", "expired-token", ""} {
		rec := g.do("GET", "/admin/profile", session, nil, merge(map[string]string{"HX-Request": "true"}))
		if rec.Code != 404 || rec.Header().Get("HX-Refresh") != "true" {
			t.Errorf("session %q: %d, HX-Refresh %q, want 404 and true", session, rec.Code, rec.Header().Get("HX-Refresh"))
		}
		if stripNonce(rec.Body.String()) != stripNonce(plain.Body.String()) && session == "owner-token" {
			t.Errorf("the htmx refusal body differs from the plain refusal")
		}
	}
}

// Plain HTTP on a non-localhost host sends no Sec-Fetch-* headers, so every
// admin request is refused. The operator is told once per ten minutes.
func TestNoFetchMetadataWarnsOnce(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	g := newRig(t, func(d *Deps) { d.Logger = slog.New(slog.NewTextHandler(&buf, nil)) })
	g.realTokens()
	bare := map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": "", "Sec-Fetch-Dest": ""}
	for range 2 {
		if c := g.do("GET", "/admin/profile", "owner-token", nil, bare).Code; c != 404 {
			t.Fatalf("no fetch metadata: %d, want 404", c)
		}
	}
	if n := strings.Count(buf.String(), "admin request without fetch metadata refused"); n != 1 {
		t.Errorf("warnings = %d, want 1:\n%s", n, buf.String())
	}
	// HTTPS, or a request that carries the metadata, never warns.
	buf.Reset()
	g.a.noFetchMetaLogged.Store(0)
	tlsReq := httptest.NewRequest("GET", "/admin/profile", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	g.a.warnNoFetchMetadata(tlsReq)
	g.do("GET", "/admin/profile", "owner-token", nil, nil)
	if buf.Len() != 0 {
		t.Errorf("unexpected warning:\n%s", buf.String())
	}
}

// ServeMux routes on escaped segments, so the guard must decide on them too: an
// encoded slash may not turn a request into an asset or the preview frame
// while the mux still sends it to the signed-in fallback.
func TestEncodedSlashIsRefused(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.realTokens()
	id := uuid.New().String()
	for _, path := range []string{
		"/admin/_%2Fx",
		"/admin/_%2F..%2Fentities",
		"/admin/_%2fx",
		"/admin/_%5Cx",
		"/admin/entities/x/edit%2Fpreview/y",
		"/admin/entities/" + id + "/edit%2Fpreview/tok",
		"/admin/entities/" + id + "/edit/preview%2Ftok",
	} {
		for name, hdr := range map[string]map[string]string{
			"fetch":  {"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""},
			"iframe": {"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe", "X-CSRF-Token": ""},
		} {
			rec := g.do("GET", path, "owner-token", nil, hdr)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d, want 404", name, path, rec.Code)
			}
			if b := rec.Body.String(); strings.Contains(b, "Owner Olga") || strings.Contains(b, "owner@example.com") {
				t.Errorf("%s %s: the body is a signed-in shell", name, path)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors 'self'") {
				t.Errorf("%s %s: frameable: %q", name, path, csp)
			}
		}
	}
}

// The guard class comes from the group it is installed on, not from the path:
// an admin mounted at a root route's first segment keeps the strict class.
func TestGuardClassFollowsScopeNotPath(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"/plugin-ui", "/auth"} {
		var g *rig
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("%s: boot panics on duplicate routes (fails closed): %v", prefix, r)
				}
			}()
			g = newRig(t, func(d *Deps) { d.AdminPath = prefix })
		}()
		if g == nil {
			continue
		}
		g.realTokens()
		fetch := map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""}
		form := map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "same-origin", "X-CSRF-Token": ""}
		for _, path := range []string{prefix + "/theme", prefix + "/logout"} {
			// A bare /plugin-ui/logout is the mux's own trailing-slash redirect (the
			// root plugin route), which runs before any guard; the followed request is refused.
			rec := g.do("GET", path, "owner-token", nil, fetch)
			if rec.Code == http.StatusTemporaryRedirect {
				rec = g.do("GET", rec.Header().Get("Location"), "owner-token", nil, fetch)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s tokenless fetch GET %s: %d, want refused", prefix, path, rec.Code)
			}
			if rec := g.do("POST", path, "owner-token", url.Values{"theme": {"dark"}}, form); rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
				t.Errorf("%s tokenless form POST %s: %d, want refused", prefix, path, rec.Code)
			}
		}
		doc := g.do("GET", prefix+"/profile", "owner-token", nil, map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
		h := doc.Header()
		if h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s admin document headers: %v", prefix, h)
		}
	}
}

// Anything in the root scope that is not one of its routes is refused.
func TestRootScopeUnknownPathRefused(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	r := httptestReq("GET", "/other", "owner-token")
	r.Header.Set("Sec-Fetch-Dest", "script")
	called := false
	h := g.a.guardFor(scopeRoot)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	if rec := serve(h, r, nil); called || rec.Code != http.StatusNotFound {
		t.Errorf("unknown root path: called=%v code=%d", called, rec.Code)
	}
}

// Every refusal is identical: the asset and plugin-ui classes seal it too.
func TestEveryRefusalIsSealed(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, c := range []struct{ name, path string }{
		{"asset encoded separator", "/admin/_/..%2Fx"},
		{"plugin-ui refused", "/plugin-ui/hello/index.html"}, // a document, not a frame
		{"admin refused", "/admin/profile"},
		{"root misc refused", "/-/edit"},
	} {
		rec := g.do("GET", c.path, "owner-token", nil, map[string]string{"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "X-CSRF-Token": ""})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want a refusal", c.name, rec.Code)
		}
		h := rec.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: refusal headers %v", c.name, h)
		}
	}
}
