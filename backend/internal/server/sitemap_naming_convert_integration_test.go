//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/localeconv"
)

var sitemapLocRe = regexp.MustCompile(`<loc>([^<]*)</loc>`)

// TestSitemapNamingAcrossConversion_Integration converts the region fixture
// with the naming left on auto (the converter removes the per-region sitemap
// groups, so it picks legacy) and checks the public sitemap: the index lists
// the same file names as before, every old URL is still listed, a group-mode
// name redirects to the index (301, not cached), a typo is a 404, and the
// revert brings the original names back.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run SitemapNaming
func TestSitemapNamingAcrossConversion_Integration(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set - skipping sitemap naming integration test")
	}
	dsn := scratchMigratedDB(t, base)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "sitemap-naming")
	t.Setenv("HATAMOTO_SITE_NAME", "Sitemap Naming")
	t.Setenv("PUBLIC_BASE_URL", "https://sitemap-naming.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Sitemap Naming', 'en')
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = '[]'::jsonb`)
	convSeed(t, exec, convFixture())

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, db, logger)
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec
	}
	// capture returns the child file names of the index and every <loc> of every child.
	capture := func() (names, locs []string) {
		t.Helper()
		idx := get("/sitemap.xml")
		if idx.Code != http.StatusOK {
			t.Fatalf("GET /sitemap.xml = %d", idx.Code)
		}
		for _, m := range sitemapLocRe.FindAllStringSubmatch(idx.Body.String(), -1) {
			name := path.Base(m[1])
			names = append(names, name)
			child := get("/" + name)
			if child.Code != http.StatusOK {
				t.Fatalf("GET /%s = %d", name, child.Code)
			}
			for _, l := range sitemapLocRe.FindAllStringSubmatch(child.Body.String(), -1) {
				locs = append(locs, l[1])
			}
		}
		slices.Sort(names)
		return names, locs
	}
	beforeNames, beforeLocs := capture()
	if len(beforeNames) < 4 || len(beforeLocs) == 0 {
		t.Fatalf("before: names %v, %d locs", beforeNames, len(beforeLocs))
	}

	svc := localeconv.NewService(db.Pool(), logger)
	plan, warnings, err := svc.Suggest(ctx)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("suggest = %v, %+v", err, warnings)
	}
	plan.StripAttributes = []string{"region_root"}
	if plan.SitemapNaming != "" {
		t.Fatalf("suggest set sitemap_naming %q, want it left to auto", plan.SitemapNaming)
	}
	rec, _, err := svc.Apply(ctx, plan, localeconv.ApplyOpts{By: "cli"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	srv.invalidateSiteSettings(ctx) // what the admin runs after an apply or revert

	afterNames, afterLocs := capture()
	if !slices.Equal(afterNames, beforeNames) {
		t.Errorf("index files after conversion = %v, want the pre-conversion names %v", afterNames, beforeNames)
	}
	// The site root is the one URL that legitimately leaves the sitemap: the
	// conversion clears the homepage setting and, with prefix-all, "/" now
	// redirects to the default locale's home, which the sitemap lists.
	root := get("/")
	if root.Code != http.StatusFound {
		t.Errorf("GET / after the conversion = %d, want 302", root.Code)
	}
	for _, loc := range beforeLocs {
		if loc == "https://sitemap-naming.example.test/" {
			continue
		}
		if !slices.Contains(afterLocs, loc) {
			t.Errorf("pre-conversion <loc> %s is in no file after the conversion", loc)
		}
	}

	// A group-mode name no longer exists: a permanent redirect to the index, never cached.
	for _, name := range []string{"sitemap-xx-post.xml", "sitemap-xx-page.xml"} {
		red := get("/" + name)
		if red.Code != http.StatusMovedPermanently || red.Header().Get("Location") != "/sitemap.xml" {
			t.Errorf("GET /%s = %d -> %q, want 301 -> /sitemap.xml", name, red.Code, red.Header().Get("Location"))
		}
		if cc := red.Header().Get("Cache-Control"); cc != "no-store" || red.Header().Get("X-Cache") != "" {
			t.Errorf("GET /%s redirect must be no-store: Cache-Control %q, X-Cache %q", name, cc, red.Header().Get("X-Cache"))
		}
	}
	if typo := get("/sitemap-zzz.xml"); typo.Code != http.StatusNotFound {
		t.Errorf("GET /sitemap-zzz.xml = %d, want 404", typo.Code)
	}

	// Revert: the original names are back and the legacy file names are gone.
	if _, err := svc.Revert(ctx, rec.ID, false, "cli"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	srv.invalidateSiteSettings(ctx)
	revertedNames, _ := capture()
	if !slices.Equal(revertedNames, beforeNames) {
		t.Errorf("index files after revert = %v, want %v", revertedNames, beforeNames)
	}
}
