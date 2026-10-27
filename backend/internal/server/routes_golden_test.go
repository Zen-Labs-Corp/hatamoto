package server

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRouteTableMatchesChiGolden checks testdata/routes.golden, recorded from
// the chi router before the stdlib migration (METHOD PATH -> handler), in both
// directions: every old route must exist in the ServeMux table, reaching the
// same handler, once the pattern syntax is normalised; and every pattern in the
// table must come from the golden or be one of the allowed additions below.
func TestRouteTableMatchesChiGolden(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range table {
		got[r.Pattern] = strings.TrimPrefix(r.Handler, "hatamoto.dev/core/internal/server.")
	}

	raw, err := os.ReadFile("testdata/routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	expected := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		old, handler, _ := strings.Cut(line, " -> ")
		method, path, _ := strings.Cut(old, " ")
		// chi.Handle (any method) became GET only; chi needed explicit HEAD
		// routes, which a "GET" pattern now covers.
		methods := []string{method}
		if method == "*" || method == "HEAD" {
			methods = []string{"GET"}
		}
		path, anyHandler := normalisePath(path)
		for _, m := range methods {
			pattern := m + " " + path
			expected[pattern] = true
			h, ok := got[pattern]
			switch {
			case !ok:
				t.Errorf("%s: no route %q", line, pattern)
			case !anyHandler && h != handler:
				t.Errorf("%s: %q reaches %s", line, pattern, h)
			}
			checked++
		}
	}
	if checked < 150 {
		t.Fatalf("golden covers only %d routes", checked)
	}

	for pattern := range got {
		if !expected[pattern] && !allowedExtraPattern(pattern, expected) {
			t.Errorf("route %q is not in the golden: add it there (or to the allowlist) if intended", pattern)
		}
	}
}

// allowedExtraPattern reports the ServeMux-only additions: a group root's alias
// without the trailing slash (chi Mount served both), and the fixed catch-alls (also at
// the bare mount prefix, so no redirect is needed)
// that answer 404 for unowned paths.
func allowedExtraPattern(pattern string, expected map[string]bool) bool {
	method, path, _ := strings.Cut(pattern, " ")
	if expected[method+" "+path+"/{$}"] {
		return true
	}
	if path == "/admin" || strings.HasPrefix(path, "/admin/") {
		return true // the Go admin; its table is pinned by testdata/admin_routes.golden
	}
	if method == "OPTIONS" { // preflight for sandboxed (opaque origin) pages, see public_cors.go
		return strings.HasSuffix(path, "/forms/{slug}/token") || strings.HasSuffix(path, "/forms/{slug}/submit") || strings.HasSuffix(path, "/track")
	}
	switch path {
	case "/feed.xml", "/atom.xml": // v1-07 post feeds, root only
		return method == "GET"
	case "/favicon.ico", "/favicon-16.png", "/favicon-32.png", "/apple-icon.png", "/icon-192.png", "/icon-512.png":
		return method == "GET" // v1-08 site icons, served from the theme
	case "/plugin-ui/_sdk/bridge.js", "/plugin-ui/{plugin}/{file...}": // v1-18 plugin admin pages (sandboxed iframe)
		return method == "GET"
	case "/auth/{rest...}": // v1-15: the old Next sign-in URLs redirect to /admin/login
		return method == "GET"
	case "/mcp": // v1-20 MCP endpoint (streamable HTTP)
		return method == "POST" || method == "GET" || method == "DELETE"
	case "/{rest...}", "/api/v1/{rest...}", "/api/v1/public/{rest...}",
		"/api/v1", "/api/v1/{$}", "/api/v1/public", "/api/v1/public/{$}":
		return slices.Contains(catchAllMethods, method)
	}
	return false
}

// normalisePath maps a chi route path to its ServeMux pattern path. anyHandler
// is set for the two routes whose handler moved into a dispatcher.
func normalisePath(p string) (path string, anyHandler bool) {
	switch {
	case p == "/":
		return "/{$}", false
	case strings.HasSuffix(p, "/*"):
		return strings.TrimSuffix(p, "*") + "{rest...}", false
	case strings.HasSuffix(p, "/"):
		return p + "{$}", false
	case p == "/sitemap-{sub}.xml":
		// Root: "/{file}" dispatches sitemap children to the same handler and
		// every other single-segment path to the page catch-all.
		return "/{file}", true
	case strings.HasSuffix(p, "/sitemap-{sub}.xml"):
		// Public: the same, with other single segments going to the 404 catch-all.
		return strings.TrimSuffix(p, "sitemap-{sub}.xml") + "{file}", true
	}
	return p, false
}
