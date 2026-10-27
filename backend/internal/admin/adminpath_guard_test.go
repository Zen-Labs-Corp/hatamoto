package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The admin address comes from one place (Admin.AdminPath, default
// DefaultAdminPath). These tests keep a hard-coded "/admin" from creeping back
// into anything the admin serves or emits.

// adminLiteral matches /admin as the start of a URL path anywhere in a line, in
// every file type: a quoted literal, a mid-string "?next=/admin/x", a template
// "/admin${p}", "/admin%s" or "%s/admin", markup like <code>/admin</code> or url(/admin).
// The segment must end at any character that cannot continue a name (or at the
// end of the line), and must not follow a word character, "-" or "." (a "%d"
// verb is the one exception), so scripts/admin-qa, import paths
// ("hatamoto.dev/core/internal/admin") and "hatamoto/admin/flash" never match.
var adminLiteral = regexp.MustCompile(`(^|[^\w.-]|%[a-z])/admin([^\w-]|$)`)

// adminAllow lists the only lines that may hold the literal. Each entry names
// the file (slash-separated, relative to the scan root's module path suffix),
// the exact trimmed line, and why. An entry covers one occurrence: a second
// identical line, or any other literal in the same file, still fails.
var adminAllow = []struct{ file, line, why string }{
	{"internal/admin/admin.go", `const DefaultAdminPath = "/admin"`, "the single source of truth for the default prefix"},
	{"web/admin/js/adminpath.js", `return (v || "/admin").replace(/\/+$/, "");`, "default when the admin-base meta is absent"},
	{"web/admin/js/blogeditor.js", `const adminBase = () => (globalThis.document?.querySelector?.('meta[name="admin-base"]')?.content || "/admin").replace(/\/+$/, "");`, "file-local copy of adminpath.js: blog-state.test.mjs stubs every other @hm/* module with fixed exports"},
	{"web/admin/js/aia/policy.js", `const adminBase = () => (globalThis.document?.querySelector?.('meta[name="admin-base"]')?.content || "/admin").replace(/\/+$/, "");`, "file-local copy of adminpath.js: aia-policy.test.mjs maps only @hm/aia/*"},
}

// commentPair is one block-comment syntax: its opener and closer.
type commentPair struct{ open, close string }

// commentPairs are the block comments of a file type. In Go, JS and CSS an
// opener counts only at the start of a line, so a "/*" inside a string such as
// "/*/delete*" never opens a comment.
func commentPairs(ext string) (pairs []commentPair, lineStart bool) {
	switch ext {
	case ".go", ".js", ".css":
		return []commentPair{{"/*", "*/"}}, true
	case ".html":
		return []commentPair{{"<!--", "-->"}, {"{{/*", "*/}}"}}, false
	}
	return nil, false
}

// codePart returns the part of line that is not comment. closer carries the
// closing token of a block comment still open from an earlier line (empty when
// none) and is updated. A line that closes a comment and then has more content
// keeps that content; a line that is only comment returns "". A "//" comment
// is dropped from the first " //" on.
func codePart(ext, line string, closer *string) string {
	pairs, lineStart := commentPairs(ext)
	if (ext == ".go" || ext == ".js") && *closer == "" {
		// A whole-line comment, or a trailing one after whitespace ("http://" stays).
		if i := strings.Index(line, "//"); i == 0 || (i > 0 && (line[i-1] == ' ' || line[i-1] == '\t')) {
			line = line[:i]
		}
	}
	var out strings.Builder
	rest := line
	for {
		if *closer != "" {
			i := strings.Index(rest, *closer)
			if i < 0 {
				return out.String()
			}
			rest, *closer = rest[i+len(*closer):], ""
			continue
		}
		best, bestAt := commentPair{}, -1
		for _, p := range pairs {
			i := strings.Index(rest, p.open)
			if lineStart && i >= 0 && strings.TrimSpace(line[:len(line)-len(rest)+i]) != "" {
				i = -1
			}
			if i >= 0 && (bestAt < 0 || i < bestAt) {
				best, bestAt = p, i
			}
		}
		if bestAt < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:bestAt])
		rest, *closer = rest[bestAt+len(best.open):], best.close
	}
}

// lineViolates reports whether one source line hard-codes the admin prefix.
// ext is the file extension with its dot; closer is the open-comment state
// shared across the lines of one file (see codePart).
func lineViolates(ext, line string, closer *string) bool {
	switch ext {
	case ".go", ".js", ".html", ".css":
		return adminLiteral.MatchString(codePart(ext, line, closer))
	}
	return false
}

func TestUILintNoAdminLiteral(t *testing.T) {
	t.Parallel()

	// Roots are relative to this package directory, with the module-relative
	// prefix used to match allowlist entries.
	type root struct{ dir, rel string }
	roots := []root{
		{".", "internal/admin"},
		{"../server", "internal/server"},
		{"../../web/admin/layouts", "web/admin/layouts"},
		{"../../web/admin/pages", "web/admin/pages"},
		{"../../web/admin/partials", "web/admin/partials"},
		{"../../web/admin/js", "web/admin/js"},
		{"../../web/admin/css", "web/admin/css"},
		{"../../web/admin/schemaform", "web/admin/schemaform"},
	}
	// In internal/server only the admin mount files are in scope.
	serverInScope := func(base string) bool {
		return base == "render_admin_handlers.go" || strings.HasPrefix(base, "admin_")
	}

	used := make([]bool, len(adminAllow))
	for _, r := range roots {
		err := filepath.WalkDir(r.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := d.Name()
			if d.IsDir() {
				if base == "testdata" || base == "node_modules" || (r.rel == "internal/server" && path != r.dir) {
					return fs.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(base)
			if strings.HasSuffix(base, "_test.go") {
				return nil
			}
			if r.rel == "internal/server" && (ext != ".go" || !serverInScope(base)) {
				return nil
			}
			switch ext {
			case ".go", ".js", ".html", ".css":
			default:
				return nil
			}
			rel, err := filepath.Rel(r.dir, path)
			if err != nil {
				return err
			}
			mod := r.rel + "/" + filepath.ToSlash(rel)
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			closer := ""
			for i, line := range strings.Split(string(b), "\n") {
				if !lineViolates(ext, line, &closer) {
					continue
				}
				allowed := false
				for j, a := range adminAllow {
					if !used[j] && a.file == mod && a.line == strings.TrimSpace(line) {
						used[j], allowed = true, true
						break
					}
				}
				if !allowed {
					t.Errorf("%s:%d hard-codes the admin path, build it from the prefix (a.url, adminPath, adminBase): %s", mod, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for j, a := range adminAllow {
		if a.why == "" {
			t.Errorf("allowlist entry for %s has no reason", a.file)
		}
		if !used[j] {
			t.Errorf("stale allowlist entry for %s (%s): remove it", a.file, a.line)
		}
	}
}

func TestUILintNoAdminLiteralMatcher(t *testing.T) {
	t.Parallel()

	// Each case is one file's lines; the last line is the one that must be caught
	// (or, for clean, every line must pass).
	caught := []struct {
		ext   string
		lines []string
	}{
		{".go", []string{`x := "/admin/foo"`}},
		{".go", []string{`http.Redirect(w, r, "/admin", 303)`}},
		{".go", []string{"p := `/admin/x`"}},
		{".go", []string{`fmt.Sprintf("/admin%s", p)`}},
		{".go", []string{`w.Header().Set("Location", "Location: /admin/x")`}},
		{".go", []string{`s.logger.Warn("open /admin/setup and enter the code")`}},
		{".go", []string{`fmt.Sprintf("%s/admin/reset", b)`}},
		{".go", []string{`msg := "Open /admin/settings/email to configure mail"`}},
		{".go", []string{`x := "/admin "`}},
		{".go", []string{`/* c */ x := "/admin/x"`}},
		{".go", []string{`x := "/admin/x" // trailing comment`}},
		{".go", []string{`/* a`, `b */ x := "/admin/x"`}},
		{".js", []string{"fetch(`/admin/${id}`)"}},
		{".js", []string{"fetch(`/admin${path}`)"}},
		{".js", []string{`location.href = '/admin/media';`}},
		{".js", []string{`const u = "?next=/admin/x";`}},
		{".js", []string{"const u = `${o}/admin/x`;"}},
		{".js", []string{`/* c */ const u = "/admin/x";`}},
		{".js", []string{`const u = "/admin" + p;`}},
		{".html", []string{`<a href="/admin/x">x</a>`}},
		{".html", []string{`{{template "hm/button" dict "Href" "/admin/users"}}`}},
		{".html", []string{`{{printf "/admin%s" .P}}`}},
		{".html", []string{`<a href="/admin{{.Path}}">x</a>`}},
		{".html", []string{`<a href=/admin>x</a>`}},
		{".html", []string{"{{ `/admin` }}"}},
		{".html", []string{`<code>/admin</code>`}},
		{".html", []string{`{{/* c */}}<a href="/admin/x">`}},
		{".html", []string{`<!-- c --><a href="/admin/x">`}},
		{".html", []string{`<!-- a`, `b --><a href="/admin/x">`}},
		{".html", []string{`go to /admin, then /x`}},
		{".go", []string{`s.logger.Info("setup done, open /admin\n")`}},
		{".go", []string{`msg := "Sign in at /admin."`}},
		{".go", []string{`x := "href_matches\":\"/admin*"`}},
		{".go", []string{`x := "/admin:"`}},
		{".go", []string{`x := "[/admin]"`}},
		{".go", []string{`x := "/admin&x=1"`}},
		{".go", []string{`x := "/admin=1"`}},
		{".go", []string{`x := "/admin!"`}},
		{".go", []string{`x := "/admin|x"`}},
		{".go", []string{`fmt.Sprintf("%d/admin", n)`}},
		{".js", []string{`const j = "{\"u\":\"\/admin\/x\"}";`}},
		{".css", []string{`a { background: url(/admin/assets/x.png) }`}},
		{".css", []string{`a { background: url(/admin) }`}},
		{".css", []string{`*{background:url(/admin/x.png)}`}},
		{".css", []string{`/* c */ a{background:url(/admin/x.png)}`}},
	}
	for _, c := range caught {
		var closer string
		got := false
		for _, l := range c.lines {
			got = lineViolates(c.ext, l, &closer)
		}
		if !got {
			t.Errorf("%s not caught: %q", c.ext, c.lines)
		}
	}

	clean := []struct {
		ext   string
		lines []string
	}{
		{".go", []string{`// redirects to "/admin/login"`}},
		{".go", []string{`import "hatamoto/backend/internal/admin"`}},
		{".go", []string{`import "hatamoto.dev/core/internal/admin"`}},
		{".go", []string{`const flashDomain = "hatamoto/admin/flash"`}},
		{".go", []string{`// scripts/admin-qa drives this`}},
		{".go", []string{`/* the "/admin" default */`}},
		{".go", []string{`Base string // /admin/settings/submissions`}},
		{".go", []string{`u := "http://localhost:8080/x" // not an admin literal`}},
		{".go", []string{`/*`, ` * the "/admin" default`, ` */`}},
		{".go", []string{`"/*/delete*", "/*/export.csv*",`}},
		{".go", []string{`x := "scripts/admin-qa/x"`}},
		{".go", []string{`x := "/administrator"`}},
		{".js", []string{`// the "/admin" default`}},
		{".js", []string{`import { adminURL } from "@hm/adminpath";`}},
		{".js", []string{`const s = "scripts/admin-qa/x";`}},
		{".js", []string{`/**`, ` * posts to /admin/x`, ` */`}},
		{".html", []string{`<!-- href="/admin/x" -->`}},
		{".html", []string{`{{/* href="/admin/x" */}}`}},
		{".html", []string{`<!-- a`, `/admin/x`, `-->`}},
		{".html", []string{`<a href="{{adminPath}}/x">x</a>`}},
		{".html", []string{`see scripts/admin-qa/run`}},
		{".css", []string{`/* published at /admin/assets/tokens.css */`}},
		{".css", []string{`/*`, ` * published at /admin/assets/tokens.css`, ` */`}},
	}
	for _, c := range clean {
		var closer string
		for _, l := range c.lines {
			if lineViolates(c.ext, l, &closer) {
				t.Errorf("%s wrongly caught: %q (line %q)", c.ext, c.lines, l)
			}
		}
	}
}

// defaultPathRx matches the DefaultAdminPath identifier.
var defaultPathRx = regexp.MustCompile(`\bDefaultAdminPath\b`)

// defaultPathAllow lists the only non-test lines that may name DefaultAdminPath.
// Everything else takes the prefix from the Admin (a.adminPath, a.url); a
// helper that pins the default belongs in defaults_test.go.
var defaultPathAllow = []struct{ file, line, why string }{
	{"internal/admin/admin.go", `const DefaultAdminPath = "/admin"`, "the single source of truth for the default prefix"},
	{"internal/admin/admin.go", `if d.Demo.Enabled && adminPath != DefaultAdminPath {`, "demo refusals only match the default prefix; New fails closed"},
	{"internal/admin/admin.go", `return nil, fmt.Errorf("admin: demo mode needs the default admin path %s, got %q", DefaultAdminPath, adminPath)`, "the demo refusal's message"},
	{"internal/admin/adminpath.go", `return DefaultAdminPath, nil`, "an empty AdminPath means the default"},
	{"internal/admin/styleguide.go", `const sgGuessable = DefaultAdminPath`, "the danger-confirm sample shows the spec copy about the default address, not the live prefix"},
	{"internal/admin/render.go", `return DefaultAdminPath`, "a View built without an Admin (error pages in tests) falls back to the default"},
}

// TestUILintNoDefaultAdminPath keeps production code from pinning the default
// prefix: DefaultAdminPath may appear in non-test code only on the allowlisted
// lines, so a new caller of a default-pinning helper fails here.
func TestUILintNoDefaultAdminPath(t *testing.T) {
	t.Parallel()

	type root struct {
		dir, rel string
		inScope  func(base string) bool
	}
	all := func(string) bool { return true }
	roots := []root{
		{".", "internal/admin", all},
		{"../server", "internal/server", func(b string) bool {
			return b == "render_admin_handlers.go" || b == "form_handlers.go" || strings.HasPrefix(b, "admin_")
		}},
		{"../../cmd", "cmd", all},
	}
	used := make([]bool, len(defaultPathAllow))
	for _, r := range roots {
		err := filepath.WalkDir(r.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := d.Name()
			if d.IsDir() {
				if base == "testdata" || (r.rel == "internal/server" && path != r.dir) {
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(base) != ".go" || strings.HasSuffix(base, "_test.go") || !r.inScope(base) {
				return nil
			}
			rel, err := filepath.Rel(r.dir, path)
			if err != nil {
				return err
			}
			mod := r.rel + "/" + filepath.ToSlash(rel)
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				trimmed := strings.TrimSpace(line)
				if !mentionsDefaultAdminPath(trimmed) {
					continue
				}
				allowed := false
				for j, a := range defaultPathAllow {
					if !used[j] && a.file == mod && a.line == trimmed {
						used[j], allowed = true, true
						break
					}
				}
				if !allowed {
					t.Errorf("%s:%d names DefaultAdminPath, take the prefix from the Admin (a.adminPath, a.url): %s", mod, i+1, trimmed)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for j, a := range defaultPathAllow {
		if a.why == "" {
			t.Errorf("allowlist entry for %s has no reason", a.file)
		}
		if !used[j] {
			t.Errorf("stale allowlist entry for %s (%s): remove it", a.file, a.line)
		}
	}
}

// mentionsDefaultAdminPath reports whether a trimmed Go line uses the
// identifier in code; whole-line comments may name it.
func mentionsDefaultAdminPath(trimmed string) bool {
	return !strings.HasPrefix(trimmed, "//") && defaultPathRx.MatchString(trimmed)
}

func TestUILintNoDefaultAdminPathMatcher(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		`return PickBannerFor(DefaultAdminPath, in, now)`,
		`func SafeNext(raw string) string { return SafeNextFor(DefaultAdminPath, raw) }`,
		`const landingPath = DefaultAdminPath + "/entities"`,
		`x := admin.DefaultAdminPath + "/y"`,
	} {
		if !mentionsDefaultAdminPath(line) {
			t.Errorf("not caught: %s", line)
		}
	}
	for _, line := range []string{
		`// DefaultAdminPath is where the admin lives`,
		`return a.adminPath`,
		`x := MyDefaultAdminPathX`,
	} {
		if mentionsDefaultAdminPath(line) {
			t.Errorf("wrongly caught: %s", line)
		}
	}
}
