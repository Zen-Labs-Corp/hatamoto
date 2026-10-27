package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/middleware"
)

// TestMiddlewareChainGolden pins, per request (method + concrete path), the gates
// that guard it, in order: auth, csrf, role[...], rl(rate,burst), tenant,
// capture. A route that loses a RequireRole or a rate limiter fails here even
// though it still exists (the route golden would not notice). The file was
// generated on this router after diffing the same 234 requests against the chi
// tree at 9b47906 (origin/main's backend): identical except two intended
// changes, an unmatched sub-path of a group ("GET /api/v1/users/p1/extra") now
// 404s before any gate, and HEAD runs its GET route's chain ("HEAD /api/v1/me").
// The request set is every line of testdata/routes.golden, the "/x" alias of
// every "/x/" group root, and edge cases. v1-16 removed the plugin mount points and their probes.
// Regenerate with UPDATE_GOLDEN=1 after a deliberate change.
func TestMiddlewareChainGolden(t *testing.T) {
	h, s := bootRoutes(t)
	token := ownerToken(t, s)

	raw, err := os.ReadFile("testdata/routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	run := func(method, path string) { out = append(out, chainOf(h, token, method, path)) }
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		old, _, _ := strings.Cut(line, " -> ")
		method, path, _ := strings.Cut(old, " ")
		if method == "*" {
			method = "GET"
		}
		p := concretePath(path)
		run(method, p)
		if strings.HasSuffix(p, "/") && p != "/" {
			run(method, strings.TrimSuffix(p, "/"))
		}
	}
	for _, x := range [][2]string{
		{"GET", "/api/v1/nope"}, {"POST", "/api/v1/nope"}, {"GET", "/api/v1/public/nope"},
		{"GET", "/api/v1/users/p1/extra"}, {"DELETE", "/api/v1/public/site"},
		{"GET", "/api/v1/entities/p1/terms"}, {"PUT", "/api/v1/entities/p1/terms"},
		{"GET", "/api/v1/settings/site"}, {"PATCH", "/api/v1/settings/site"},
		{"HEAD", "/api/v1/me"}, {"HEAD", "/api/v1/users/"}, {"HEAD", "/sitemap-en.xml"}, {"HEAD", "/theme/assets/x.css"},
		{"POST", "/theme/assets/x.css"}, {"GET", "/foo"}, {"GET", "/foo/bar"}, {"HEAD", "/foo/bar"},
		{"OPTIONS", "/api/v1/me"},
	} {
		run(x[0], x[1])
	}
	sort.Strings(out)
	got := strings.Join(out, "\n") + "\n"

	const golden = "testdata/middleware.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		inWant := map[string]bool{}
		for _, l := range wl {
			inWant[l] = true
		}
		inGot := map[string]bool{}
		for _, l := range gl {
			inGot[l] = true
			if !inWant[l] {
				t.Errorf("new:     %s", l)
			}
		}
		for _, l := range wl {
			if !inGot[l] {
				t.Errorf("missing: %s", l)
			}
		}
	}
}

type traceKey struct{}

// chainOf serves one request as the owner with every gate observing, and
// returns "METHOD path => [tags]".
func chainOf(h http.Handler, token, method, path string) string {
	var tags []string
	middleware.TraceHook = func(r *http.Request, tag string) {
		*r.Context().Value(traceKey{}).(*[]string) = append(*r.Context().Value(traceKey{}).(*[]string), tag)
	}
	defer func() { middleware.TraceHook = nil }()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", token)
	req = req.WithContext(context.WithValue(req.Context(), traceKey{}, &tags))
	func() {
		defer func() { _ = recover() }() // core handlers hit the shell's nil services
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	return fmt.Sprintf("%s %s => %v", method, path, tags)
}

// concretePath fills a chi-style golden path with sample values.
func concretePath(path string) string {
	path = strings.ReplaceAll(path, "sitemap-{sub}.xml", "sitemap-en.xml")
	if strings.HasSuffix(path, "/*") {
		path = strings.TrimSuffix(path, "*") + "a/b.js"
	}
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			return path
		}
		j := strings.Index(path[i:], "}")
		path = path[:i] + "p1" + path[i+j+1:]
	}
}
