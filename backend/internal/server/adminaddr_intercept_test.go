package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/middleware"
)

const testPrefix = "/k7qm-x2rf-9tdc"

const (
	bodyRefused = "REFUSED"
	bodyNext    = "NEXT"
	bodyPublic  = "PUBLIC404"
)

// interceptHarness wraps a stub next: "/public-ok" answers 200, every other
// path outside the intercept answers 404, and a path under the admin prefix
// that reaches it answers 200 NEXT.
type interceptHarness struct {
	deps     interceptDeps
	handler  http.Handler
	signedIn bool
	owner    bool
	invite   string
	clock    *fakeClock
}

func newHarness(prefix string, perMin int) *interceptHarness {
	h := &interceptHarness{owner: true}
	h.deps = interceptDeps{
		prefix:      func() string { return prefix },
		signedIn:    func(*http.Request) bool { return h.signedIn },
		hasOwner:    func(context.Context) bool { return h.owner },
		inviteValid: func(_ context.Context, tok string) bool { return tok != "" && tok == h.invite },
		refuse: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, bodyRefused)
		},
	}
	if perMin > 0 {
		th, clk := newTestThrottle(perMin, 2*time.Minute)
		h.deps.throttle, h.clock = th, clk
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == "GET" || r.Method == "HEAD") && !isCleanPath(r.URL.EscapedPath()):
			// Like the real mux: redirect to the clean path.
			http.Redirect(w, r, path.Clean(r.URL.EscapedPath()), http.StatusTemporaryRedirect)
		case r.URL.Path == "/public-ok":
			_, _ = io.WriteString(w, "PUBLIC200")
		case underPrefix(r.URL.Path, prefix):
			_, _ = io.WriteString(w, bodyNext)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, bodyPublic)
		}
	})
	h.handler = interceptMiddleware(h.deps)(next)
	return h
}

func (h *interceptHarness) do(method, target, ip string, mut ...func(*http.Request)) (int, string, *httptest.ResponseRecorder) {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = ip + ":4000"
	for _, m := range mut {
		m(r)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String(), rec
}

func (h *interceptHarness) get(target string) (int, string) {
	code, body, _ := h.do("GET", target, "198.51.100.1")
	return code, body
}

func TestInterceptWellKnown(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, p := range []string{"/wp-login.php", "/wp-login.php/", "/WP-Login.PHP", "/.env", "/.git/config", "/xmlrpc.php",
		"/wp-admin/admin-ajax.php", "/phpmyadmin/index.php", "/user/login", "/ghost", "/cgi-bin/x"} {
		if code, body := h.get(p); code != 404 || body != bodyRefused {
			t.Errorf("%s = %d %q, want refused", p, code, body)
		}
	}
	if code, body := h.get("/public-ok"); code != 200 || body != "PUBLIC200" {
		t.Errorf("public page = %d %q", code, body)
	}
	// Ordinary unknown paths are not probes: next answers.
	if _, body := h.get("/no-such-page-1"); body != bodyPublic {
		t.Errorf("unknown public path = %q, want the public 404", body)
	}
}

func TestInterceptOldAddressAndAuth(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, p := range []string{"/admin", "/admin/", "/admin/login", "/admin/settings", "/auth/x", "/auth"} {
		code, body, rec := h.do("GET", p, "198.51.100.1")
		if code != 404 || body != bodyRefused || rec.Header().Get("Location") != "" {
			t.Errorf("%s = %d %q Location=%q, want a plain refused 404", p, code, body, rec.Header().Get("Location"))
		}
	}
	// Signed in does not help on the old address.
	h.signedIn = true
	if code, _ := h.get("/admin/settings"); code != 404 {
		t.Errorf("signed-in old address = %d", code)
	}
}

// With the default address, upgraded installs keep working as before: every
// request at or under /admin reaches the admin, signed in or out, blocked or
// not, however the path is written. Probes elsewhere and /auth stay refused.
// Fails if the /admin pass-through is removed.
func TestInterceptDefaultAddressPassesEverythingThrough(t *testing.T) {
	h := newHarness("/admin", 2)
	const ip = "198.51.100.9"
	for _, p := range []string{"/admin", "/admin/", "/admin/login", "/admin/settings", "/admin/entities/x", "/admin/setup",
		"/admin/accept", "/admin/theme/menus", "/admin/_", "/admin/%2fsettings", "/admin/_%2fx", "/admin/preview/theme/x"} {
		for _, m := range []string{"GET", "HEAD", "POST"} {
			if code, body, _ := h.do(m, p, ip); code != 200 || body != bodyNext && m != "HEAD" {
				t.Errorf("signed-out %s %s = %d %q, want the admin", m, p, code, body)
			}
		}
	}
	// Unclean and encoded-dot shapes go to the mux, never to a refusal.
	for _, p := range []string{"/admin//x", "/admin/./x", "/admin/x/..", "/admin/%2e%2e/x", "/admin/%2E/settings"} {
		if _, body, _ := h.do("GET", p, ip); body == bodyRefused {
			t.Errorf("GET %s was refused under the default address", p)
		}
	}
	// Block the client with public 404s: /admin is still not hidden.
	for i := 0; i < 5; i++ {
		h.do("GET", "/no-such-page", ip)
	}
	if !h.deps.throttle.blocked(middleware.IPBucket(ip)) {
		t.Fatal("client not blocked by public 404s")
	}
	for _, p := range []string{"/admin", "/admin/login", "/admin/settings"} {
		if code, body, _ := h.do("GET", p, ip); code != 200 || body != bodyNext {
			t.Errorf("blocked %s = %d %q, want the admin", p, code, body)
		}
	}
	// Probes outside /admin and /auth are refused as with any address.
	for _, p := range []string{"/wp-admin", "/wp-login.php", "/.env", "/auth/x", "/auth", "/administrator"} {
		if code, body, _ := h.do("GET", p, "198.51.100.10"); code != 404 || body != bodyRefused {
			t.Errorf("%s = %d %q, want refused", p, code, body)
		}
	}
	// Signed out deep links reach the admin (which redirects to login).
	if code, body := newHarness("/admin", 0).get("/admin/settings"); code != 200 || body != bodyNext {
		t.Errorf("signed-out /admin/settings = %d %q", code, body)
	}
}

// Documents the leak the intercept exists to close: the real chain answers
// /admin for anyone with a redirect into the admin (to the login page), so
// the address must be answered before the mux.
func TestMuxRedirectsAdminTodayBaseline(t *testing.T) {
	full, _ := bootRoutes(t)
	rec := httptest.NewRecorder()
	// A browser navigation: the sealed-admin guard refuses anything else with a bare 404.
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	full.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); rec.Code < 300 || rec.Code > 399 || !strings.HasPrefix(loc, "/admin/") {
		t.Fatalf("baseline GET /admin = %d Location=%q, want a redirect into /admin/", rec.Code, loc)
	}
	h := newHarness(testPrefix, 0)
	if code, _, rec := h.do("GET", "/admin", "198.51.100.1"); code != 404 || rec.Header().Get("Location") != "" {
		t.Errorf("intercepted /admin = %d", code)
	}
}

func TestInterceptSignedOutAllowList(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, p := range []string{"", "/", "/login", "/forgot", "/reset", "/_/admin.css", "/_/a/b.js"} {
		if code, body := h.get(testPrefix + p); code != 200 || body != bodyNext {
			t.Errorf("%s = %d %q, want pass-through", p, code, body)
		}
	}
	for _, p := range []string{"/settings", "/entities", "/entities/x", "/setup", "/accept", "/login/", "/login/x",
		"/_", "/_styleguide"} {
		if code, body := h.get(testPrefix + p); code != 404 || body != bodyRefused {
			t.Errorf("%q = %d %q, want refused", p, code, body)
		}
	}
	// A dot segment never reaches the admin: the mux redirects it first.
	if code, _ := h.get(testPrefix + "/_/../settings"); code != 307 {
		t.Errorf("/_/../settings = %d, want the mux redirect", code)
	}
	// A sibling that merely starts with the prefix is not under it.
	if _, body := h.get(testPrefix + "x/settings"); body != bodyPublic {
		t.Errorf("sibling path = %q", body)
	}
}

func TestInterceptAcceptToken(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.invite = "good-token"
	if code, _ := h.get(testPrefix + "/accept?token=good-token"); code != 200 {
		t.Errorf("valid token via query = %d", code)
	}
	for _, p := range []string{"/accept", "/accept?token=", "/accept?token=bad"} {
		if code, _ := h.get(testPrefix + p); code != 404 {
			t.Errorf("%s = %d, want 404", p, code)
		}
	}
	post := func(body string) (int, string) {
		code, got, _ := h.do("POST", testPrefix+"/accept", "198.51.100.1", func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		})
		return code, got
	}
	if code, _ := post(url.Values{"token": {"good-token"}, "name": {"x"}}.Encode()); code != 200 {
		t.Errorf("valid token via form = %d", code)
	}
	if code, _ := post("token=nope"); code != 404 {
		t.Errorf("bad form token = %d", code)
	}
	if code, _ := post("token=good-token&pad=" + strings.Repeat("a", maxInviteFormBytes)); code != 404 {
		t.Errorf("oversized form = %d, want 404", code)
	}
}

func TestInterceptBodyRestoredForAdmin(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.invite = "t1"
	var seen string
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
	}))
	r := httptest.NewRequest("POST", testPrefix+"/accept", bytes.NewBufferString("token=t1&name=Ann"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "token=t1&name=Ann" {
		t.Errorf("admin saw body %q", seen)
	}
}

func TestInterceptSetupOnlyWithoutOwner(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.owner = true
	if code, _ := h.get(testPrefix + "/setup"); code != 404 {
		t.Errorf("setup with owner = %d", code)
	}
	h.owner = false
	if code, body := h.get(testPrefix + "/setup"); code != 200 || body != bodyNext {
		t.Errorf("setup without owner = %d %q", code, body)
	}
}

func TestInterceptSignedInPassesThrough(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.signedIn = true
	for _, p := range []string{"", "/", "/settings", "/entities/x", "/anything"} {
		if code, body := h.get(testPrefix + p); code != 200 || body != bodyNext {
			t.Errorf("%q signed in = %d %q", p, code, body)
		}
	}
}

func TestInterceptChargesOrdinaryPublic404(t *testing.T) {
	const n = 5
	h := newHarness(testPrefix, n)
	ip, other := "203.0.113.7", "203.0.113.8"
	for i := 0; i < n; i++ {
		if code, body, _ := h.do("GET", "/random-"+string(rune('a'+i)), ip); code != 404 || body != bodyPublic {
			t.Fatalf("public 404 %d = %d %q", i, code, body)
		}
	}
	if h.deps.throttle.blocked(ip) {
		t.Fatalf("blocked after %d, want the N+1th to block", n)
	}
	h.do("GET", "/random-z", ip) // N+1
	if !h.deps.throttle.blocked(ip) {
		t.Fatal("not blocked after N+1 public 404s")
	}
	// Blocked: the admin address (even login) and probes get the standard 404, never 429.
	for _, p := range []string{testPrefix + "/login", testPrefix + "/anything", testPrefix, "/wp-login.php"} {
		if code, body, _ := h.do("GET", p, ip); code != 404 || body != bodyRefused {
			t.Errorf("blocked %s = %d %q", p, code, body)
		}
	}
	// A valid admin session is not locked out, but a probe is still refused.
	h.signedIn = true
	if code, body, _ := h.do("GET", testPrefix+"/settings", ip); code != 200 || body != bodyNext {
		t.Errorf("blocked but signed in = %d %q, want the admin", code, body)
	}
	if code, body, _ := h.do("GET", "/wp-login.php", ip); code != 404 || body != bodyRefused {
		t.Errorf("blocked signed-in probe = %d %q", code, body)
	}
	h.signedIn = false
	// Public pages still answer, and another IP is unaffected.
	if code, _, _ := h.do("GET", "/public-ok", ip); code != 200 {
		t.Errorf("public page for blocked IP = %d", code)
	}
	if code, body, _ := h.do("GET", testPrefix+"/login", other); code != 200 || body != bodyNext {
		t.Errorf("other IP login = %d %q", code, body)
	}
	// The block expires.
	h.clock.t = h.clock.t.Add(2*time.Minute + time.Second)
	if code, body, _ := h.do("GET", testPrefix+"/login", ip); code != 200 || body != bodyNext {
		t.Errorf("after expiry login = %d %q", code, body)
	}
}

func TestInterceptChargesRefusedProbes(t *testing.T) {
	h := newHarness(testPrefix, 3)
	ip := "203.0.113.9"
	for i := 0; i < 4; i++ {
		h.do("GET", "/wp-login.php", ip)
	}
	if !h.deps.throttle.blocked(ip) {
		t.Error("refused probes did not block")
	}
}

func TestInterceptThrottleDisabled(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for i := 0; i < 300; i++ {
		h.do("GET", "/random-x", "203.0.113.7")
	}
	if code, body, _ := h.do("GET", testPrefix+"/login", "203.0.113.7"); code != 200 || body != bodyNext {
		t.Errorf("login after many 404s with the throttle off = %d %q", code, body)
	}
}

func TestInterceptCountsProbeNamesOnly(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(testPrefix, 0)
	h.deps.probes = newProbeCounter(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(h.deps.probes.Stop)
	h.handler = interceptMiddleware(h.deps)(http.NotFoundHandler())
	for _, p := range []string{"/wp-login.php?secret=1", "/wp-login.php", "/.env"} {
		h.do("GET", p, "198.51.100.1")
	}
	h.deps.probes.flush()
	out := buf.String()
	for _, want := range []string{"name=wp-login", "count=2", "name=env", "count=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") || strings.Contains(out, ".php") || strings.Contains(out, "/") {
		t.Errorf("log leaks a path or query:\n%s", out)
	}
}

func TestWellKnownListSane(t *testing.T) {
	seen := map[string]string{}
	for name, paths := range wellKnownGroups {
		if name == "" || strings.Contains(name, "/") {
			t.Errorf("bad probe name %q", name)
		}
		for _, p := range paths {
			if !strings.HasPrefix(p, "/") {
				t.Errorf("%q lacks a leading slash", p)
			}
			k := strings.ToLower(strings.TrimSuffix(p, "/"))
			if prev, dup := seen[k]; dup {
				t.Errorf("%q listed under %s and %s", p, prev, name)
			}
			seen[k] = name
		}
	}
	if len(seen) < 200 {
		t.Errorf("only %d well-known paths", len(seen))
	}
}

// A path the core itself serves must never be on the list.
func TestWellKnownNeverShadowsCoreRoutes(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range table {
		p := rt.Pattern
		if i := strings.IndexByte(p, ' '); i >= 0 {
			p = p[i+1:]
		}
		if strings.ContainsAny(p, "{}") || strings.HasPrefix(p, "/admin") {
			continue
		}
		if name, ok := wellKnownName(p); ok {
			t.Errorf("core route %q is on the well-known list (%s)", rt.Pattern, name)
		}
	}
}

func TestNewInterceptDepsWithoutCollaborators(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	d := s.newInterceptDeps()
	t.Cleanup(d.probes.Stop)
	if d.prefix() != "/admin" {
		t.Errorf("prefix = %q", d.prefix())
	}
	r := httptest.NewRequest("GET", "/admin/login", nil)
	if d.signedIn(r) || d.inviteValid(r.Context(), "x") || !d.hasOwner(r.Context()) {
		t.Error("nil collaborators must answer no session, no invite, owner present")
	}
	s.cfg.Admin.Resolved = testPrefix
	if s.adminPathValue() != testPrefix {
		t.Error("resolved prefix ignored")
	}
}

// An address that the scanner list also names must still be the admin: signed
// out it reaches the signed-out allow list, signed in it reaches the admin.
func TestInterceptAddressOnScannerListIsAdmin(t *testing.T) {
	addrs := []string{"/administrator", "/manager", "/filemanager", "/phpmyadmin-x7k2q9", "/ghost", "/backoffice",
		"/cpanel", "/typo3", "/wp-admin", "/solr", "/actuator", "/vendor"}
	for _, addr := range addrs {
		h := newHarness(addr, 0)
		if code, body := h.get(addr + "/login"); code != 200 || body != bodyNext {
			t.Errorf("signed out %s/login = %d %q, want the admin", addr, code, body)
		}
		if code, body := h.get(addr + "/settings"); code != 404 || body != bodyRefused {
			t.Errorf("signed out %s/settings = %d %q, want the admin refusal", addr, code, body)
		}
		h.signedIn = true
		for _, p := range []string{addr, addr + "/", addr + "/settings", addr + "/entities/x"} {
			if code, body := h.get(p); code != 200 || body != bodyNext {
				t.Errorf("signed in %s = %d %q, want the admin", p, code, body)
			}
		}
	}
}

// Probe prefixes match at a segment boundary only.
func TestWellKnownPrefixNeedsSegmentBoundary(t *testing.T) {
	for _, p := range []string{"/phpmyadmin-x7k2q9", "/phpmyadmin-x7k2q9/login", "/ghostwriter", "/typo3x/a"} {
		if name, ok := wellKnownName(p); ok {
			t.Errorf("%s matched the %s probe", p, name)
		}
	}
	for _, p := range []string{"/phpmyadmin", "/phpmyadmin/", "/phpmyadmin/x/y", "/ghost/api", "/.env.bak2", "/wp-admin/x"} {
		if _, ok := wellKnownName(p); !ok {
			t.Errorf("%s should be a probe", p)
		}
	}
}

func TestInterceptPreviewFrameSignedOut(t *testing.T) {
	const id = "0b6f1c1e-5a53-4c7e-9a4e-7b7d6a1d2f33"
	tok := strings.Repeat("aB3_-", 6) + "xy" // 32 characters
	frame := testPrefix + "/entities/" + id + "/edit/preview/" + tok
	h := newHarness(testPrefix, 0)
	for _, m := range []string{"GET", "HEAD"} {
		if code, body, _ := h.do(m, frame, "198.51.100.1"); code != 200 || body != bodyNext && m == "GET" {
			t.Errorf("%s frame = %d %q, want the admin", m, code, body)
		}
	}
	refused := []string{
		testPrefix + "/entities/" + id + "/edit/preview/" + tok + "/x",
		testPrefix + "/entities/" + id + "/edit/preview/" + tok[:31],
		testPrefix + "/entities/" + id + "/edit/preview/" + tok[:31] + ".",
		testPrefix + "/entities/" + id + "/edit/preview/" + tok[:31] + "%2e",
		testPrefix + "/entities/" + id + "/edit/preview/",
		testPrefix + "/entities/not-a-uuid/edit/preview/" + tok,
		testPrefix + "/entities/{" + id + "}/edit/preview/" + tok,
		testPrefix + "/entities/" + id + "/edit",
		testPrefix + "/entities/" + id + "/edit/preview",
	}
	for _, p := range refused {
		if code, body := h.get(p); code != 404 || body != bodyRefused {
			t.Errorf("GET %s = %d %q, want refused", p, code, body)
		}
	}
	for _, p := range []string{
		testPrefix + "/entities/" + id + "/edit/preview/..",
		testPrefix + "/entities/x/../" + id + "/edit/preview/" + tok,
	} {
		if code, _ := h.get(p); code != 307 {
			t.Errorf("GET %s = %d, want the mux redirect", p, code)
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if code, body, _ := h.do(m, frame, "198.51.100.1"); code != 404 || body != bodyRefused {
			t.Errorf("%s frame = %d %q, want refused", m, code, body)
		}
	}
	// The editor's own POST (staging) stays behind the session.
	if code, body, _ := h.do("POST", testPrefix+"/entities/"+id+"/edit/preview", "198.51.100.1"); code != 404 || body != bodyRefused {
		t.Errorf("signed-out stage POST = %d %q", code, body)
	}
}

// Words a site can serve as a page or section are not probes.
func TestWellKnownKeepsLegitimateSections(t *testing.T) {
	for _, p := range []string{"/status", "/horizon", "/telescope", "/vendor/acme", "/manager/team", "/solr", "/grafana",
		"/gitlab", "/webmail", "/cpanel", "/backoffice", "/filemanager", "/typo3", "/typo3/guide", "/bitrix/x", "/mysql",
		"/phpinfo", "/swagger", "/owa", "/remote/login", "/xmlrpc", "/wp", "/actuator", "/node/add", "/user/register"} {
		if name, ok := wellKnownName(p); ok {
			t.Errorf("%s is on the list (%s)", p, name)
		}
	}
	for _, p := range []string{"/administrator", "/user/login", "/ghost", "/.env", "/wp-login.php", "/xmlrpc.php",
		"/phpmyadmin", "/pma", "/auth", "/auth/x", "/manager/html", "/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php"} {
		if _, ok := wellKnownName(p); !ok {
			t.Errorf("%s should still be a probe", p)
		}
	}
}

// The always-allowed pages are answered without a session lookup.
func TestInterceptSkipsSessionLookupForAllowedPaths(t *testing.T) {
	h := newHarness(testPrefix, 0)
	lookups := 0
	h.deps.signedIn = func(*http.Request) bool { lookups++; return false }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, bodyNext)
	}))
	for _, p := range []string{"", "/", "/login", "/forgot", "/reset", "/_/admin.css"} {
		if code, body := h.get(testPrefix + p); code != 200 || body != bodyNext {
			t.Errorf("%q = %d %q", p, code, body)
		}
	}
	if lookups != 0 {
		t.Errorf("%d session lookups for always-allowed paths, want 0", lookups)
	}
	// A page that needs a decision still checks the session first.
	h.get(testPrefix + "/settings")
	if lookups != 1 {
		t.Errorf("lookups = %d after /settings, want 1", lookups)
	}
}

// throttleHarness returns deps with a throttle on a fixed clock; tests build
// the handler around their own next.
func throttleHarness(perMin int) (*interceptDeps, http.Handler) {
	th, _ := newTestThrottle(perMin, 2*time.Minute)
	d := &interceptDeps{
		prefix:      func() string { return testPrefix },
		signedIn:    func(*http.Request) bool { return false },
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		throttle:    th,
	}
	return d, interceptMiddleware(*d)(http.NotFoundHandler())
}

func serveFrom(h http.Handler, remote, target string, hdr map[string]string) int {
	r := httptest.NewRequest("GET", target, nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

func TestInterceptChargesFinal405(t *testing.T) {
	d, _ := throttleHarness(3)
	h := interceptMiddleware(*d)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	for i := 0; i < 4; i++ {
		serveFrom(h, "203.0.113.5:1", "/x", nil)
	}
	if !d.throttle.blocked(middleware.IPBucket("203.0.113.5")) {
		t.Error("405 responses did not charge the throttle")
	}
}

// Every client behind one private or loopback peer shares its address, with or
// without forwarded headers: nobody is charged or blocked, the login stays
// reachable, and the operator is told.
func TestInterceptSkipsThrottleForInternalClients(t *testing.T) {
	for _, c := range []struct {
		name, remote string
		hdr          map[string]string
		warns        int // calls of the unidentified hook (20 requests with the headers, then one login without)
	}{
		{"private, no header", "172.18.0.1:5000", nil, 21},
		{"loopback, no header", "127.0.0.1:5000", nil, 0},
		{"private, spoofed header", "172.18.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.77"}, 21},
		{"ula", "[fd00::5]:5000", nil, 21},
		{"cgnat (tailscale)", "100.101.102.103:5000", nil, 21},
		{"loopback, forwarded", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "127.0.0.1"}, 20},
		{"loopback, real ip", "[::1]:5000", map[string]string{"X-Real-IP": "::1"}, 20},
		{"mapped private", "[::ffff:10.0.0.2]:5000", nil, 21},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _ := throttleHarness(3)
			warned := 0
			d.unidentified = func() { warned++ }
			h := interceptMiddleware(*d)(http.NotFoundHandler())
			for i := 0; i < 20; i++ {
				serveFrom(h, c.remote, "/random", c.hdr)
			}
			// A third client is not locked out of the login.
			other := interceptMiddleware(*d)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, bodyNext) }))
			rec := httptest.NewRecorder()
			r := httptest.NewRequest("GET", testPrefix+"/login", nil)
			r.RemoteAddr = c.remote
			other.ServeHTTP(rec, r)
			if rec.Body.String() != bodyNext {
				t.Fatalf("login = %q, want the admin", rec.Body.String())
			}
			if warned != c.warns {
				t.Errorf("unidentified hook called %d times, want %d (once per request, the caller dedupes; none for a plain local one)", warned, c.warns)
			}
		})
	}
}

// Identified public clients are still throttled, also with a spoofed header
// naming a private address.
func TestInterceptThrottlesIdentifiedClients(t *testing.T) {
	d, _ := throttleHarness(3)
	h := interceptMiddleware(*d)(http.NotFoundHandler())
	for i := 0; i < 4; i++ {
		serveFrom(h, "203.0.113.5:1", "/random", map[string]string{"X-Forwarded-For": "10.0.0.1"})
	}
	if !d.throttle.blocked(middleware.IPBucket("203.0.113.5")) {
		t.Error("a public peer with spoofed headers must still be throttled")
	}
}

func TestInternalAddress(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.0.0.2": true, "172.18.0.1": true, "192.168.1.5": true,
		"100.64.0.1": true, "100.127.255.254": true, "100.63.255.255": false, "100.128.0.1": false, "::ffff:100.100.1.1": true,
		"fd00::5": true, "fe80::1": true, "169.254.1.1": true, "::ffff:10.0.0.2": true, "::ffff:127.0.0.1": true,
		"203.0.113.9": false, "2001:db8::1": false, "::ffff:203.0.113.9": false, "64:ff9b::a00:1": false, "": false, "junk": false,
	} {
		if got := internalAddress(ip); got != want {
			t.Errorf("internalAddress(%q) = %v, want %v", ip, got, want)
		}
	}
}

func TestThrottleLogsCountWhenABlockStarts(t *testing.T) {
	th, _ := newTestThrottle(2, time.Minute)
	var got []int
	th.onBlock = func(n int) { got = append(got, n) }
	for i := 0; i < 3; i++ {
		th.charge("a")
	}
	th.charge("a") // already blocked: no second line
	for i := 0; i < 3; i++ {
		th.charge("b")
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("onBlock counts = %v, want [1 2]", got)
	}
}

// The wired log line carries a count only.
func TestNewInterceptDepsLogsBlockWithoutIdentifiers(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&buf, nil))
	s.cfg.Admin.NotFoundPerMin, s.cfg.Admin.NotFoundBlock = 1, time.Minute
	d := s.newInterceptDeps()
	t.Cleanup(d.probes.Stop)
	d.throttle.charge("203.0.113.5")
	d.throttle.charge("203.0.113.5")
	out := buf.String()
	if !strings.Contains(out, "blocked_clients=1") || strings.Contains(out, "203.0.113") {
		t.Errorf("log = %q", out)
	}
	d.unidentified()
	d.unidentified()
	if n := strings.Count(buf.String(), "HATAMOTO_TRUSTED_PROXIES"); n != 1 {
		t.Errorf("proxy warning logged %d times, want once", n)
	}
}

// An unclean GET or HEAD path is the mux's to redirect, wherever it points:
// refusing it would answer 404 where the public site answers 307, and an
// uncharged redirect would make that difference a free oracle for the address.
func TestInterceptPassesUncleanPathsToTheMux(t *testing.T) {
	h := newHarness(testPrefix, 3)
	for _, p := range []string{
		testPrefix + "//settings", testPrefix + "/./x", testPrefix + "/x/..", testPrefix + "/x//", testPrefix + "//",
		"/admin//x", "/wp-admin/../x", "/wp-login.php//", "/.env/./x", "/zz//x",
	} {
		for _, m := range []string{"GET", "HEAD"} {
			if code, _, _ := h.do(m, p, "198.51.100.1"); code != http.StatusTemporaryRedirect {
				t.Errorf("%s %s = %d, want the mux redirect", m, p, code)
			}
		}
	}
	// Redirects charge nothing, and the clean form is still classified.
	if h.deps.throttle.blocked(middleware.IPBucket("198.51.100.1")) {
		t.Error("redirects charged the throttle")
	}
	if code, body := h.get(testPrefix + "/settings"); code != 404 || body != bodyRefused {
		t.Errorf("clean form = %d %q, want refused", code, body)
	}
	// A single trailing slash is clean, as for the mux.
	if code, body := h.get("/wp-admin/"); code != 404 || body != bodyRefused {
		t.Errorf("/wp-admin/ = %d %q, want refused", code, body)
	}
}

// Paths under /api and /media get the public JSON or media 404, not the themed
// one, so a well-known entry there would fingerprint the refusal.
func TestWellKnownNotUnderJSONNotFoundPrefixes(t *testing.T) {
	for name, paths := range wellKnownGroups {
		for _, p := range paths {
			if isReservedPath(strings.TrimSuffix(p, "/")) {
				t.Errorf("%s: %q sits under a prefix with its own 404", name, p)
			}
		}
	}
	for _, w := range wellKnownPrefixes {
		if isReservedPath(strings.TrimSuffix(w.Prefix, "/")) {
			t.Errorf("prefix %q sits under a prefix with its own 404", w.Prefix)
		}
	}
}

// A blocked client holding a session costs one lookup, not two.
func TestInterceptLooksUpTheSessionOnce(t *testing.T) {
	h := newHarness(testPrefix, 2)
	lookups := 0
	inner := h.deps.signedIn
	h.deps.signedIn = func(r *http.Request) bool { lookups++; return inner(r) }
	h.handler = interceptMiddleware(h.deps)(http.NotFoundHandler())
	for i := 0; i < 5; i++ {
		h.do("GET", "/random", "198.51.100.1")
	}
	if !h.deps.throttle.blocked(middleware.IPBucket("198.51.100.1")) {
		t.Fatal("client not blocked")
	}
	lookups = 0
	h.signedIn = true
	if code, body, _ := h.do("GET", testPrefix+"/settings", "198.51.100.1"); code != 404 || body != "404 page not found\n" {
		t.Fatalf("signed-in blocked client got %d %q, want the admin (via next)", code, body)
	}
	if lookups != 1 {
		t.Errorf("session lookups = %d, want 1", lookups)
	}
}

// A path that is clean to the mux but unclean once decoded (%2e%2e, %2e, %2f)
// is not redirected by it, so the intercept must classify it: passing it on
// would let the admin answer it signed out.
func TestInterceptClassifiesEncodedDotSegments(t *testing.T) {
	h := newHarness(testPrefix, 3)
	for _, p := range []string{
		testPrefix + "/%2e%2e/x", testPrefix + "/%2e/settings", testPrefix + "/%2fsettings", testPrefix + "/%2E%2E/x",
		testPrefix + "/x/%2e%2E", testPrefix + "/_/%2e%2e/settings", testPrefix + "/%2e./x",
		"/admin/%2e%2e/x", "/admin/%2E/settings", "/wp-admin/%2e%2e/x", "/.git/%2fconfig",
	} {
		for _, m := range []string{"GET", "HEAD"} {
			if code, body, _ := h.do(m, p, "198.51.100.1"); code != 404 || (m == "GET" && body != bodyRefused) {
				t.Errorf("%s %s = %d %q, want refused", m, p, code, body)
			}
		}
	}
	if !h.deps.throttle.blocked(middleware.IPBucket("198.51.100.1")) {
		t.Error("refusals were not charged to the throttle")
	}
	// A wrong guess reaches the public site.
	h = newHarness(testPrefix, 3)
	if code, body := h.get("/zz-guess/%2e%2e/x"); code != 404 || body != bodyPublic {
		t.Errorf("wrong guess = %d %q, want the public 404", code, body)
	}
}

func TestMuxRedirectsUnclean(t *testing.T) {
	for _, c := range []struct {
		method, target string
		want           bool
	}{
		{"GET", "/a//b", true}, {"GET", "/a/./b", true}, {"GET", "/a/../b", true}, {"GET", "/a/b//", true},
		{"GET", "/a/b/", false}, {"GET", "/a/b", false}, {"GET", "/", false},
		{"GET", "/a/%2e%2e/b", false}, {"GET", "/a/%2e/b", false}, {"GET", "/a/%2fb", false}, {"GET", "/a/%2E%2E/b", false},
		{"CONNECT", "/a//b", false},
	} {
		r := httptest.NewRequest(c.method, c.target, nil)
		if got := muxRedirectsUnclean(r); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.target, got, c.want)
		}
	}
}

// An unclean path that cleans to an admin path is answered by the intercept
// with the clean-path redirect, never by the mux: the mux would first try its
// trailing-slash redirect (/<addr>/_ to /<addr>/_/), which a wrong
// guess does not get, and that answer is uncharged and ignores the block.
func TestInterceptCleanRedirectForAdminClasses(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, c := range []struct{ target, location string }{
		{testPrefix + "/./_", testPrefix + "/_"},
		{testPrefix + "/_/.", testPrefix + "/_"},
		{testPrefix + "/_/x/..", testPrefix + "/_"},
		{testPrefix + "//login?next=1", testPrefix + "/login?next=1"},
		{"/admin/./x", "/admin/x"},
		{"/wp-login.php/.", "/wp-login.php"},
		{"/./wp-login.php/", "/wp-login.php/"},
	} {
		// Each class is answered here: next must not see the request.
		saw := false
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { saw = true })
		mw := interceptMiddleware(h.deps)(next)
		for _, m := range []string{"GET", "HEAD"} {
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, httptest.NewRequest(m, c.target, nil))
			if saw || rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != c.location {
				t.Errorf("%s %s = %d Location %q (next reached %v), want 307 %q", m, c.target, rec.Code, rec.Header().Get("Location"), saw, c.location)
			}
		}
	}
	// A path outside the classes still goes to the mux.
	saw := false
	mw := interceptMiddleware(h.deps)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { saw = true }))
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/zz-guess/./_", nil))
	if !saw {
		t.Error("an unclean path outside the admin classes did not reach the mux")
	}
}

// Signed out under the address, an encoded slash is refused and charged; with
// a session it reaches the admin.
func TestInterceptRefusesEncodedSlashSignedOut(t *testing.T) {
	h := newHarness(testPrefix, 3)
	for _, p := range []string{
		testPrefix + "/_%2fx", testPrefix + "/_%2Fx.css",
		testPrefix + "/entities%2f6ba7b810-9dad-11d1-80b4-00c04fd430c8/edit/preview/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		testPrefix + "/entities/6ba7b810-9dad-11d1-80b4-00c04fd430c8%2fedit/preview/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		testPrefix + "%2flogin",
	} {
		if code, body, _ := h.do("GET", p, "198.51.100.1"); code != 404 || body != bodyRefused {
			t.Errorf("GET %s = %d %q, want refused", p, code, body)
		}
	}
	if !h.deps.throttle.blocked(middleware.IPBucket("198.51.100.1")) {
		t.Error("refusals were not charged")
	}
	h = newHarness(testPrefix, 3)
	h.signedIn = true
	if code, body := h.get(testPrefix + "/_%2fx"); code != 200 || body != bodyNext {
		t.Errorf("signed in = %d %q, want the admin", code, body)
	}
}

// Assets live under <addr>/_/ only. The old <addr>/assets/ is an ordinary
// admin path: refused signed out, handed to the admin (which 404s it) signed in.
func TestInterceptOldAssetsPathRefused(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, p := range []string{"/assets/admin.css", "/assets/a/b.js"} {
		if code, body := h.get(testPrefix + p); code != 404 || body != bodyRefused {
			t.Errorf("signed out %s = %d %q, want refused", p, code, body)
		}
	}
	h.signedIn = true
	if code, body := h.get(testPrefix + "/assets/admin.css"); code != 200 || body != bodyNext {
		t.Errorf("signed in /assets/admin.css = %d %q, want the admin to answer", code, body)
	}
	for _, p := range []string{"/_/../settings", "/_/%2e%2e/settings"} {
		h.signedIn = false
		if code, body := h.get(testPrefix + p); body == bodyNext && code == 200 {
			t.Errorf("signed out %s reached the admin", p)
		}
	}
}

const oldTestPrefix = "/q3zx-m8wd-7hrt"

// An address the admin used to have: signed out it is the standard refusal,
// signed in it is the hint page (404, no-store, not charged to the throttle).
func TestInterceptOldAddressHint(t *testing.T) {
	h := newHarness(testPrefix, 3)
	h.deps.oldPrefixes = func() []string { return []string{oldTestPrefix} }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, bodyNext)
	}))
	for _, p := range []string{oldTestPrefix, oldTestPrefix + "/", oldTestPrefix + "/login", oldTestPrefix + "/entities/x"} {
		if code, body := h.get(p); code != 404 || body != bodyRefused {
			t.Errorf("signed out %s = %d %q, want the standard refusal", p, code, body)
		}
	}
}

func TestInterceptOldAddressSignedInGetsHintNotCharged(t *testing.T) {
	h := newHarness(testPrefix, 3)
	h.deps.oldPrefixes = func() []string { return []string{oldTestPrefix} }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, bodyNext)
	}))
	h.signedIn = true
	for _, m := range []string{"GET", "HEAD", "POST"} {
		for i := 0; i < 10; i++ { // far over the 3 a minute budget
			code, body, rec := h.do(m, oldTestPrefix+"/settings", "198.51.100.1")
			if code != 404 || rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("%s hint = %d %v", m, code, rec.Header())
			}
			if !strings.Contains(body, "The admin address changed; check your email.") || strings.Contains(body, oldTestPrefix) || strings.Contains(body, "<link") || strings.Contains(body, "<script") {
				t.Fatalf("%s hint body = %q", m, body)
			}
		}
	}
	// Not charged: the same client, signed out, still reaches the live admin.
	h.signedIn = false
	if code, body := h.get(testPrefix + "/login"); code != 200 || body != bodyNext {
		t.Errorf("client was charged for the hint: %d %q", code, body)
	}
	// Contrast: signed-out probes of the old address are charged and block.
	for i := 0; i < 10; i++ {
		h.get(oldTestPrefix + "/x")
	}
	if code, _ := h.get(testPrefix + "/settings"); code != 404 {
		t.Errorf("signed-out old address hits were not charged: %d", code)
	}
}

// The legacy default address keeps its rule: the standard refusal for
// everyone, signed in or not.
func TestInterceptLegacyAddressStaysRefused(t *testing.T) {
	h := newHarness(testPrefix, 0)
	for _, in := range []bool{false, true} {
		h.signedIn = in
		if code, body := h.get("/admin/login"); code != 404 || body != bodyRefused {
			t.Errorf("signed in %v: /admin/login = %d %q", in, code, body)
		}
	}
	// The live address is unaffected.
	if code, body := h.get(testPrefix + "/settings"); code != 200 || body != bodyNext {
		t.Errorf("live address = %d %q", code, body)
	}
}

// A swap away from the default address remembers it, so a signed-in admin on
// the old bookmark gets the hint.
func TestInterceptRetiredDefaultAddressGetsHint(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.deps.oldPrefixes = func() []string { return []string{"/admin"} }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, bodyNext)
	}))
	h.signedIn = true
	if code, body := h.get("/admin/login"); code != 404 || !strings.Contains(body, "check your email") {
		t.Errorf("= %d %q, want the hint", code, body)
	}
}

// With no remembered addresses (a restart), a former address is an ordinary
// public path again.
func TestInterceptForgottenOldAddressIsPublic(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.deps.oldPrefixes = func() []string { return nil }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, bodyPublic)
	}))
	if code, body := h.get(oldTestPrefix + "/login"); code != 404 || body != bodyPublic {
		t.Errorf("= %d %q, want the public 404", code, body)
	}
	h.signedIn = true
	if code, body := h.get(oldTestPrefix + "/login"); code != 404 || body != bodyPublic {
		t.Errorf("signed in = %d %q, want the public 404", code, body)
	}
}

// An address that is live again is not old.
func TestInterceptRestoredAddressIsLive(t *testing.T) {
	h := newHarness(testPrefix, 0)
	h.deps.oldPrefixes = func() []string { return []string{testPrefix} }
	h.handler = interceptMiddleware(h.deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, bodyNext)
	}))
	h.signedIn = true
	if code, body := h.get(testPrefix + "/settings"); code != 200 || body != bodyNext {
		t.Errorf("= %d %q", code, body)
	}
}
