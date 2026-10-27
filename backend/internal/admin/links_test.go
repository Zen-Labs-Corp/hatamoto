package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// movedAdminPaths are the old admin prefixes of every area that has moved under
// /admin/settings, /admin/theme (or /admin/plugins), one entry per area; each route-move todo
// adds its own. A prefix matches on a segment boundary, so the new
// /admin/settings/users is not caught by /admin/users.
var movedAdminPaths = []string{
	"/admin/schemas", "/admin/taxonomies", "/admin/users", // content model and team
	"/admin/menus", "/admin/forms", // menus and forms (submissions included)
	"/admin/redirects", "/admin/linking", "/admin/sections", // SEO & URLs and section templates
	"/admin/settings/theme", "/admin/settings/plugins", "/admin/import", // appearance, plugins, import
	"/admin/settings/site", "/admin/settings/contact", "/admin/settings/homepage", "/admin/settings/api", // general
	"/admin/settings/themes", "/admin/settings/sections", "/admin/settings/menus", "/admin/settings/appearance", "/admin/settings/branding", // the Theme area
}

// TestNoOldAdminLinks guards the 301 policy from the inside: nothing the admin
// itself emits (links, form actions, redirects, emails, scripts) may point at a
// moved path, or every click and every post-save lands on a redirect hop.
func TestNoOldAdminLinks(t *testing.T) {
	t.Parallel()

	var parts []string
	for _, p := range movedAdminPaths {
		parts = append(parts, regexp.QuoteMeta(p))
	}
	// The media library list moved; /admin/media/{id}, /picker and /upload did
	// not, so only the bare path (with or without a query) is old.
	parts = append(parts, `/admin/media(?:["?#]|$)`)
	old := regexp.MustCompile(`(?:` + strings.Join(parts[:len(parts)-1], "|") + `)(?:[^A-Za-z0-9_-]|$)|` + parts[len(parts)-1])

	for _, root := range []string{"..", "../../web/admin"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := d.Name()
			if d.IsDir() {
				if base == "testdata" || base == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(base, "_test.go") || base == "legacy_redirects.go" {
				return nil
			}
			switch filepath.Ext(base) {
			case ".go", ".html", ".js", ".css":
			default:
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if old.MatchString(line) && !strings.Contains(line, "legacy-path-ok") {
					t.Errorf("%s:%d points at a moved admin path: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestRenderedPagesLinkToNewPaths checks the same thing on output: the pages of
// the moved areas contain no href or action on an old path.
func TestRenderedPagesLinkToNewPaths(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	attr := regexp.MustCompile(`(?:href|action|hx-get|hx-post|data-hm-post)="([^"]*)"`)
	for _, path := range []string{"/admin/settings/users", "/admin/settings/users/new"} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		for _, m := range attr.FindAllStringSubmatch(rec.Body.String(), -1) {
			for _, p := range movedAdminPaths {
				if m[1] == p || strings.HasPrefix(m[1], p+"/") || strings.HasPrefix(m[1], p+"?") || m[1] == "/admin/media" || strings.HasPrefix(m[1], "/admin/media?") {
					t.Errorf("%s links to %s", path, m[1])
				}
			}
		}
	}
}

// Forms, submissions and their editor pages, rendered with data, link only to
// the new paths (the submission list and detail are the busiest in links).
func TestRenderedFormsPagesLinkToNewPaths(t *testing.T) {
	t.Parallel()

	g, _, _, fm, s := submissionRig(t)
	attr := regexp.MustCompile(`(?:href|action|formaction|hx-get|hx-post|data-hm-post)="([^"]*)"`)
	for _, path := range []string{
		"/admin/settings/forms", "/admin/settings/forms/new", "/admin/settings/forms/" + fm.ID.String() + "/edit",
		"/admin/settings/submissions?form=" + fm.ID.String(), "/admin/settings/submissions/" + s.ID.String(),
		"/admin/settings/submissions/" + s.ID.String() + "/delete",
	} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		for _, m := range attr.FindAllStringSubmatch(rec.Body.String(), -1) {
			for _, p := range movedAdminPaths {
				if m[1] == p || strings.HasPrefix(m[1], p+"/") || strings.HasPrefix(m[1], p+"?") || m[1] == "/admin/media" || strings.HasPrefix(m[1], "/admin/media?") {
					t.Errorf("%s links to %s", path, m[1])
				}
			}
		}
	}
}

// TestNoHxBoost keeps the admin on real page loads: no hx-boost and no
// hx-history anywhere in its templates, scripts or Go source.
func TestNoHxBoost(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"..", "../../web/admin"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" || d.Name() == "vendor" {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), "_test.go") || strings.Contains(path, "/vendor/") {
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".html", ".js", ".css":
			default:
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, bad := range []string{"hx-boost", "hx-history"} {
				if strings.Contains(string(b), bad) {
					t.Errorf("%s uses %s", path, bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
