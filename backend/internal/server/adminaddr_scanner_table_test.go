package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/packages"
)

const tablePrefix = "/k7qm-x2rf-9tdc"

// scannerProbes are the paths scanners try, written out by family. Every one
// must be answered exactly as the public site answers an unknown path.
var scannerProbes = func() []string {
	var out []string
	add := func(ps ...string) { out = append(out, ps...) }
	// WordPress
	for _, dir := range []string{"", "/wordpress", "/blog", "/wp", "/cms", "/site", "/new", "/old", "/test"} {
		add(dir+"/wp-login.php", dir+"/wp-admin", dir+"/wp-admin/", dir+"/wp-admin/admin-ajax.php",
			dir+"/xmlrpc.php", dir+"/wp-config.php", dir+"/wp-content/plugins/x/readme.txt", dir+"/wp-json/wp/v2/users")
	}
	// Dot files and VCS
	for _, f := range []string{".env", ".env.local", ".env.production", ".env.bak", ".git/config", ".git/HEAD",
		".svn/entries", ".DS_Store", ".htaccess", ".htpasswd", ".aws/credentials", ".ssh/id_rsa", ".npmrc",
		".vscode/sftp.json", ".idea/workspace.xml", ".dockerenv", ".bash_history"} {
		add("/" + f)
	}
	// Database tools and panels
	for _, f := range []string{"phpmyadmin", "phpmyadmin/index.php", "phpMyAdmin/", "pma", "pma/index.php", "myadmin",
		"mysql", "sqladmin", "dbadmin", "adminer.php", "adminer", "cpanel", "whm", "plesk", "webmail", "roundcube"} {
		add("/" + f)
	}
	// Admin-looking names and legacy admin addresses
	add("/admin", "/admin/", "/admin/login", "/admin/settings", "/admin/entities/x", "/admin/setup", "/admin/accept",
		"/administrator", "/administrator/index.php", "/admin.php", "/admin.html", "/adminpanel", "/admin_login",
		"/admincp", "/siteadmin", "/sysadmin", "/webadmin", "/backoffice", "/controlpanel", "/user/login",
		"/user/register", "/ghost", "/ghost/api/admin", "/typo3", "/bitrix/admin", "/auth", "/auth/x", "/auth/login")
	// Config, backups, build files
	for _, f := range []string{"config.php", "config.json", "config.yml", "web.config", "settings.py", "composer.json",
		"composer.lock", "package.json", "yarn.lock", "Dockerfile", "docker-compose.yml", "backup.zip", "backup.sql",
		"backup.tar.gz", "db.sql", "dump.sql", "database.sql", "site.zip", "www.zip", "wwwroot.zip", "terraform.tfstate"} {
		add("/" + f)
	}
	// Debug, status, actuator, API docs
	for _, f := range []string{"phpinfo.php", "info.php", "test.php", "server-status", "server-info", "nginx_status",
		"_profiler", "telescope", "actuator", "actuator/env", "actuator/heapdump", "swagger-ui.html", "swagger.json",
		"v2/api-docs", "openapi.json", "elmah.axd", "trace.axd", "_ignition/health-check"} {
		add("/" + f)
	}
	// Web shells and appliances
	for _, f := range []string{"shell.php", "cmd.php", "c99.php", "r57.php", "wso.php", "eval-stdin.php",
		"cgi-bin/luci", "cgi-bin/test.cgi", "boaform/admin/formLogin", "HNAP1", "owa", "ecp",
		"autodiscover/autodiscover.xml", "remote/login", "manager/html", "solr/admin", "vendor/phpunit/x.php", "node_modules/x/index.js"} {
		add("/" + f)
	}
	// Signed-out hits under the real address that the allow list does not cover.
	for _, f := range []string{"/settings", "/entities", "/entities/x", "/users", "/setup", "/accept",
		"/accept?token=nope", "/login/", "/_", "/api/v1/x"} {
		add(tablePrefix + f)
	}
	// Unclean shapes: the public mux redirects these, so must the intercept.
	for _, f := range []string{"//x", "/./x", "/x/..", "/x//", "/.", "//", "/_/../x", "/x/./y"} {
		add(tablePrefix+f, "/admin"+f, "/wp-admin"+f, "/.git"+f, "/zz-guess"+f)
	}
	// Encoded shapes: clean to the mux (it cleans the escaped path, so it does
	// not redirect and routes them as they are), unclean once decoded.
	for _, f := range []string{"/%2e%2e/x", "/%2e/settings", "/%2fsettings", "/%2E%2E/x", "/%2E/settings", "/%2Fsettings",
		"/%2e./x", "/.%2E/x", "/x/%2e%2e", "/x/%2e%2e/", "/%2e%2e%2fx", "/x%2f%2e%2e/y", "/_/%2e%2e/settings"} {
		add(tablePrefix+f, "/admin"+f, "/wp-admin"+f, "/.git"+f, "/zz-guess"+f)
	}
	return out
}()

// tableChains returns the chain WIRE builds and the public chain, both over the
// same real mux.
//
// intercepted reproduces buildHandler's order with the intercept inserted right
// after requestIDMiddleware, before the mux (in demo mode WIRE must put it
// before demoWriteLimit and demoBanner too, so a refusal gets the demo banner
// a public 404 gets; the demo middlewares are not part of this table).
// public is buildHandler itself. Both share one Server, so they see the same
// renderer, theme preview and mux.
func tableChains(t *testing.T) (intercepted, public http.Handler, s *Server) {
	t.Helper()
	public, s = bootThemed404(t)
	s.registerNotFound()
	s.renderHandlers.preview = &themePreview{key: previewKey("table-secret"), mgr: &packages.Manager{}}
	deps := interceptDeps{
		prefix: func() string { return tablePrefix },
		signedIn: func(r *http.Request) bool {
			c, err := r.Cookie(auth.SessionCookieName)
			return err == nil && c.Value == "valid"
		},
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      s.writeStandardNotFound,
	}
	chain := []httpx.Middleware{
		s.securityHeadersMiddleware,
		middleware.RecoveryLogger(s.logger, s.redactAdminPath),
		middleware.RequestLogger(s.logger, s.redactAdminPath),
		requestIDMiddleware,
		interceptMiddleware(deps),
	}
	// The mux carries the same JSON error bodies as buildHandler.
	intercepted = httpx.Chain(chain...)(httpx.WithErrorBodies(s.mux, publicErrorBody))
	return intercepted, public, s
}

// request is one table request: method, target and the cookies it carries.
type tableReq struct {
	method, target string
	signedIn       bool
	cookies        []*http.Cookie
}

func (q tableReq) dump(h http.Handler) string {
	req := httptest.NewRequest(q.method, q.target, nil)
	if q.signedIn {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "valid"})
	}
	for _, c := range q.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return dumpResponse(rec)
}

// randomLike returns a path of the same shape as p (same segment count and
// trailing slash) that no route knows, with the query kept.
func randomLike(target string, n int) string {
	path, query, hasQuery := strings.Cut(target, "?")
	segs := strings.Split(path, "/")
	for i := range segs {
		if i > 0 && segs[i] != "" && segs[i] != "." && segs[i] != ".." && !strings.Contains(segs[i], "%") {
			segs[i] = fmt.Sprintf("no-such-page-%d-%dzq", n, i)
		}
	}
	out := strings.Join(segs, "/")
	if hasQuery {
		out += "?" + query
	}
	return out
}

// echoed is the path as the theme prints it (no query, no trailing slash).
func echoed(target string) string {
	p := strings.SplitN(target, "?", 2)[0]
	if u, err := url.PathUnescape(p); err == nil {
		p = u // the theme prints the decoded path
	}
	return strings.TrimSuffix(p, "/")
}

// echoedRaw is the request path without the query.
func echoedRaw(target string) string { return strings.SplitN(target, "?", 2)[0] }

// cleanedTarget is the path a mux redirect points to.
func cleanedTarget(target string) string {
	p := echoedRaw(target)
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c
}

// decodedClean is the decoded path cleaned with a single trailing slash kept.
func decodedClean(target string) string {
	p := echoedRaw(target)
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return cleanedTarget(p)
}

var tableMethods = []string{"GET", "HEAD", "POST", "OPTIONS", "PUT", "DELETE", "PATCH"}

// Every refused request is answered as the public chain answers an unknown path
// of the same shape, for every method, with and without the theme-preview query
// and cookie, signed out and signed in (signed in under the address passes
// through to the admin and is covered below). The path the theme echoes is the
// only allowed difference, so GET and HEAD are also compared with the public
// catch-all on the very same path.
func TestScannerTableByteIdentical(t *testing.T) {
	intercepted, public, s := tableChains(t)
	pv := s.renderHandlers.preview
	token := pv.Token("default", time.Now())
	cookie := &http.Cookie{Name: previewCookie, Value: token}
	badCookie := &http.Cookie{Name: previewCookie, Value: "x.y"}
	catchAll := s.securityHeadersMiddleware(requestIDMiddleware(http.HandlerFunc(s.renderHandlers.handleRenderCatchAll)))

	variants := []struct {
		name    string
		query   string
		cookies []*http.Cookie
		methods []string
	}{
		{"plain", "", nil, tableMethods},
		{"preview off", "?hm_theme_preview=off", nil, tableMethods},
		{"preview token", "?hm_theme_preview=" + token, nil, []string{"GET", "HEAD", "POST"}},
		{"preview cookie", "", []*http.Cookie{cookie}, []string{"GET", "HEAD", "POST", "OPTIONS"}},
		{"unsigned preview cookie", "", []*http.Cookie{badCookie}, []string{"GET"}},
	}
	compared := 0
	for _, signedIn := range []bool{false, true} {
		for i, p := range scannerProbes {
			if signedIn && underPrefix(strings.SplitN(p, "?", 2)[0], tablePrefix) {
				continue // a signed-in admin passes through
			}
			for _, v := range variants {
				for _, m := range v.methods {
					target := p
					if v.query != "" {
						target += map[bool]string{true: "&" + v.query[1:], false: v.query}[strings.Contains(p, "?")]
					}
					got := tableReq{m, target, signedIn, v.cookies}.dump(intercepted)
					random := randomLike(target, len(scannerProbes))
					want := tableReq{m, random, signedIn, v.cookies}.dump(public)
					// The echoed path (canonical, og:url, Location) is the only difference.
					swapped := strings.ReplaceAll(got, echoed(target), echoed(random))
					if c := cleanedTarget(target); c != echoed(target) {
						swapped = strings.ReplaceAll(swapped, c, cleanedTarget(random)) // a redirect's Location
					}
					if strings.Contains(target, "%") { // a redirect to the cleaned decoded path
						for _, f := range []string{"Location: %s\n", `<a href="%s">`} {
							swapped = strings.ReplaceAll(swapped, fmt.Sprintf(f, decodedClean(target)), fmt.Sprintf(f, decodedClean(random)))
						}
					}
					if swapped != want {
						t.Fatalf("probe %d %s %s (%s, signed in %v) differs from the public answer for an unknown path\n--- public (%s) ---\n%s\n--- got ---\n%s",
							i, m, target, v.name, signedIn, random, want, got)
					}
					if (m == "GET" || m == "HEAD") && v.query == "" && v.cookies == nil && isCleanPath(echoedRaw(target)) {
						if same := (tableReq{m, target, signedIn, nil}).dump(catchAll); got != same {
							t.Fatalf("probe %d %s %s differs from the public catch-all on the same path\n--- catch-all ---\n%s\n--- got ---\n%s", i, m, target, same, got)
						}
					}
					compared++
				}
			}
		}
	}
	t.Logf("compared %d requests against the public chain", compared)
	// The comparison is only meaningful if the public answers differ by method and query.
	sample := func(m, q string) string {
		return tableReq{m, "/no-such-page-zz" + q, false, nil}.dump(public)[:3]
	}
	if sample("GET", "") != "404" || sample("OPTIONS", "") != "405" || sample("GET", "?hm_theme_preview=off") != "303" || sample("POST", "") != "404" {
		t.Fatal("the public chain no longer answers 404, 405 and 303 for these requests; the table lost its meaning")
	}
}

// With the default address '/admin' as the resolved one, every probe outside
// /admin (and /auth) is still answered as the public site answers an unknown
// path of the same shape, byte for byte.
func TestScannerTableWithDefaultAddress(t *testing.T) {
	_, _, s := tableChains(t)
	deps := interceptDeps{
		prefix:      func() string { return defaultAdminPrefix },
		signedIn:    func(*http.Request) bool { return false },
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      s.writeStandardNotFound,
	}
	h := httpx.Chain(requestIDMiddleware, interceptMiddleware(deps))(httpx.WithErrorBodies(s.mux, publicErrorBody))
	pub := httpx.Chain(requestIDMiddleware)(httpx.WithErrorBodies(s.mux, publicErrorBody))
	compared := 0
	for i, p := range scannerProbes {
		if underPrefix(strings.SplitN(p, "?", 2)[0], defaultAdminPrefix) {
			continue // served by the admin under the default address
		}
		for _, m := range []string{"GET", "HEAD", "POST"} {
			random := randomLike(p, len(scannerProbes))
			got := tableReq{m, p, false, nil}.dump(h)
			want := tableReq{m, random, false, nil}.dump(pub)
			swapped := strings.ReplaceAll(got, echoed(p), echoed(random))
			if c := cleanedTarget(p); c != echoed(p) {
				swapped = strings.ReplaceAll(swapped, c, cleanedTarget(random))
			}
			if strings.Contains(p, "%") {
				for _, f := range []string{"Location: %s\n", `<a href="%s">`} {
					swapped = strings.ReplaceAll(swapped, fmt.Sprintf(f, decodedClean(p)), fmt.Sprintf(f, decodedClean(random)))
				}
			}
			if swapped != want {
				t.Fatalf("probe %d %s %s differs from the public answer\n--- public (%s) ---\n%s\n--- got ---\n%s", i, m, p, random, want, got)
			}
			compared++
		}
	}
	if compared < 500 {
		t.Fatalf("compared only %d requests", compared)
	}
}

func TestScannerTableIsWideAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range scannerProbes {
		if seen[p] {
			t.Errorf("duplicate probe %q", p)
		}
		seen[p] = true
	}
	if len(seen) < 200 {
		t.Fatalf("only %d distinct probes, want at least 200", len(seen))
	}
}

func TestScannerTableIgnoredWithValidSession(t *testing.T) {
	_, s := bootThemed404(t)
	deps := interceptDeps{
		prefix: func() string { return tablePrefix },
		signedIn: func(r *http.Request) bool {
			c, err := r.Cookie(auth.SessionCookieName)
			return err == nil && c.Value == "valid"
		},
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      s.writeStandardNotFound,
	}
	reached := ""
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.URL.Path })
	h := interceptMiddleware(deps)(inner)
	for _, p := range []string{tablePrefix, tablePrefix + "/", tablePrefix + "/settings", tablePrefix + "/entities/x"} {
		reached = ""
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "valid"})
		h.ServeHTTP(httptest.NewRecorder(), req)
		if reached != p {
			t.Errorf("signed-in %s did not reach the admin", p)
		}
	}
	// Probes stay refused with a session: not every well-known path is reachable by it.
	reached = ""
	req := httptest.NewRequest("GET", "/wp-login.php", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "valid"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if reached != "" {
		t.Error("a well-known probe reached the handler with a session")
	}
}

// The review-3 oracle through the real chain: with the admin mounted at a random address
// (standing in for the resolved address), an encoded dot segment signed out
// never reaches an admin handler.
func TestEncodedDotSegmentNeverReachesAdminSignedOut(t *testing.T) {
	_, s := wiredHandler(t, false, tablePrefix) // the real admin is mounted at tablePrefix
	deps := interceptDeps{
		prefix:      func() string { return tablePrefix },
		signedIn:    func(*http.Request) bool { return false },
		hasOwner:    func(context.Context) bool { return true },
		inviteValid: func(context.Context, string) bool { return false },
		refuse:      s.writeStandardNotFound,
	}
	h := httpx.Chain(requestIDMiddleware, interceptMiddleware(deps))(httpx.WithErrorBodies(s.mux, publicErrorBody))
	for _, p := range []string{tablePrefix + "/%2e%2e/x", tablePrefix + "/entities/%2e%2e/edit", tablePrefix + "/%2e/settings", tablePrefix + "/%2fsettings", tablePrefix + "/%2E%2E/x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("GET %s = %d Location %q, want the standard 404 (the admin answered)", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// mountedProbes are shapes under the address whose answer depends on the routes
// the real admin has there (the table above has no admin mounted at its prefix).
var mountedProbes = []string{
	"/./_", "/_/.", "/_/x/..", "/_//", "//_", "/_/./", "/./login", "/login/.", "/login/x/..",
	"/x/../login", "//login", "/./", "/.", "/./forgot", "/./reset", "/./setup", "/./accept",
	"/_%2fx", "/_%2Fx", "/_/x%2fy", "/login%2f", "/%2flogin", "/login%2fx",
	"/entities%2f6ba7b810-9dad-11d1-80b4-00c04fd430c8/edit/preview/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	"/entities/6ba7b810-9dad-11d1-80b4-00c04fd430c8%2fedit/preview/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	"/entities/6ba7b810-9dad-11d1-80b4-00c04fd430c8/edit/preview%2fAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
}

// With the real admin mounted at a random address (standing in for the resolved address,
// as the intercept prefix), each probe is answered as the same shape under a
// wrong guess, for a client that is not blocked and for one that is.
func TestScannerTableWithAdminMounted(t *testing.T) {
	_, public := wiredHandler(t, false, tablePrefix) // the real admin is mounted at tablePrefix
	for _, blocked := range []bool{false, true} {
		var th *nfThrottle
		if blocked {
			th = newNFThrottle(1, time.Minute)
		}
		deps := interceptDeps{
			prefix:      func() string { return tablePrefix },
			signedIn:    func(*http.Request) bool { return false },
			hasOwner:    func(context.Context) bool { return true },
			inviteValid: func(context.Context, string) bool { return false },
			refuse:      public.writeStandardNotFound,
			throttle:    th,
		}
		h := httpx.Chain(requestIDMiddleware, interceptMiddleware(deps))(httpx.WithErrorBodies(public.mux, publicErrorBody))
		guess := httpx.Chain(requestIDMiddleware)(httpx.WithErrorBodies(public.mux, publicErrorBody))
		do := func(h http.Handler, m, target string) string {
			req := httptest.NewRequest(m, target, nil)
			req.RemoteAddr = "198.51.100.7:4000"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return dumpResponse(rec)
		}
		if blocked {
			for i := 0; i < 4; i++ {
				do(h, "GET", fmt.Sprintf("%s/settings%d", tablePrefix, i))
			}
		}
		for _, p := range mountedProbes {
			for _, m := range []string{"GET", "HEAD"} {
				got := do(h, m, tablePrefix+p)
				want := do(guess, m, "/zz-guess"+p)
				if got != strings.ReplaceAll(want, "zz-guess", strings.TrimPrefix(tablePrefix, "/")) {
					t.Errorf("%s %s%s (blocked %v) differs from the wrong guess\n--- guess ---\n%s\n--- got ---\n%s", m, tablePrefix, p, blocked, want, got)
				}
			}
		}
	}
}
