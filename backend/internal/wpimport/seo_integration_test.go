//go:build integration

package wpimport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/seo"
)

func newSEODeps(t *testing.T) (*Deps, func(q string) string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)
	reset := func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_seo_settings`)
		d.SEO.Invalidate()
	}
	reset()
	t.Cleanup(reset)
	str := func(q string) string {
		var s string
		if err := db.Pool().QueryRow(ctx, q).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s
	}
	return d, str
}

func seoRunner(t *testing.T, d *Deps, siteURL string, mode SiteMode, opts Options) *Runner {
	t.Helper()
	opts.AllowPrivate = true
	r, err := NewRunner(d, nil, &Dump{Site: Site{URL: siteURL}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	r.state = &SiteState{Mode: mode}
	r.schemaOf["post"], r.schemaOf["page"] = "post", "page"
	return r
}

// The settings file alone: Rank Math fixture on an empty site (A1).
func TestSEOImportFileOnEmptySite_Integration(t *testing.T) {
	d, _ := newSEODeps(t)
	f, err := parseSEOFixture(t, "rankmath-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	r := seoRunner(t, d, "http://127.0.0.1:1", ModeEmpty, Options{NoSEOLive: true, SEOFile: f})
	if err := r.importSEO(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := d.SEO.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Separator != "|" || got.Types["post"].Title != "%title% %sep% %sitename%" || !got.Index.NoIndexTaxonomy["category"] ||
		got.Social.TwitterSite != "@exampleuser" || got.Verification.Google != "abc123verifytoken" {
		t.Errorf("settings = %+v", got)
	}
	res := r.res.SEO
	if res == nil || res.Plugin != "rankmath" || len(res.Applied) == 0 || len(res.Would) != 0 {
		t.Fatalf("result = %+v", res)
	}
	keys := map[string]string{}
	for _, row := range res.Applied {
		keys[row.Key] = row.Value
	}
	for k, v := range map[string]string{"separator": "|", "social.twitter_site": "@exampleuser", "verification.google": "abc123verifytoken", "noindex.taxonomy.category": "Yes"} {
		if keys[k] != v {
			t.Errorf("applied[%s] = %q, want %q", k, keys[k], v)
		}
	}
}

const seoLiveHome = `<!doctype html><html><head><title>Example Site</title>
<!-- This site is optimized with the Yoast SEO plugin v21 -->
<meta name="google-site-verification" content="live-google-code" />
<meta property="og:site_name" content="Example Site" />
<meta name="twitter:site" content="@liveuser" />
<script type="application/ld+json">{"@graph":[{"@type":"Organization","name":"Example Org","logo":{"url":"https://example.com/logo.png"},"sameAs":["https://social.example.com/example"]}]}</script>
</head><body></body></html>`

func seoLiveSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		_, _ = io.WriteString(w, seoLiveHome)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /wp-admin/\nDisallow: /private/\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The live read alone (A2), and a settings file that has one invalid value.
func TestSEOImportLiveOnly_Integration(t *testing.T) {
	d, _ := newSEODeps(t)
	srv := seoLiveSite(t)
	r := seoRunner(t, d, srv.URL, ModeEmpty, Options{})
	if err := r.importSEO(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := d.SEO.Get(context.Background())
	if got.Verification.Google != "live-google-code" || !got.Social.SiteTags || got.Social.TwitterSite != "@liveuser" {
		t.Errorf("settings = %+v %+v", got.Verification, got.Social)
	}
	if strings.Join(got.Robots.ExtraLines, "|") != "User-agent: *|Disallow: /private/" {
		t.Errorf("robots = %v", got.Robots.ExtraLines)
	}
	if res := r.res.SEO; res == nil || res.Plugin != "yoast" || len(res.Sources) != 1 || res.Sources[0] != "live" {
		t.Errorf("result = %+v", res)
	}
}

func TestSEOImportInvalidValueDropsOnlyThatField_Integration(t *testing.T) {
	d, _ := newSEODeps(t)
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{
		"pt_post_title": strings.Repeat("a", 400), "pt_page_title": "%title%", "google_verify": "code",
	}}
	r := seoRunner(t, d, "http://127.0.0.1:1", ModeEmpty, Options{NoSEOLive: true, SEOFile: f})
	if err := r.importSEO(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := d.SEO.Get(context.Background())
	if got.Types["page"].Title != "%title%" || got.Verification.Google != "code" || got.Types["post"].Title != "" {
		t.Errorf("settings = %+v", got)
	}
	if len(r.res.Warnings) != 0 || !strings.Contains(strings.Join(r.res.SEO.NotMapped, "\n"), "Title template: post") {
		t.Errorf("warnings %v notMapped %v", r.res.Warnings, r.res.SEO.NotMapped)
	}
}

// A site with content: nothing is written, differing values are listed (A4).
func TestSEOImportExistingSiteWritesNothing_Integration(t *testing.T) {
	d, str := newSEODeps(t)
	ctx := context.Background()
	if err := d.SEO.Merge(ctx, func(s *seo.Settings) { s.Separator = "–"; s.Verification.Bing = "owner-bing" }); err != nil {
		t.Fatal(err)
	}
	before := str(`SELECT settings::text FROM hatamoto_seo_settings`)
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{"title_separator": "–", "google_verify": "code", "bing_verify": "other-bing"}}
	r := seoRunner(t, d, "http://127.0.0.1:1", ModeExisting, Options{NoSEOLive: true, SEOFile: f})
	if err := r.importSEO(ctx); err != nil {
		t.Fatal(err)
	}
	if after := str(`SELECT settings::text FROM hatamoto_seo_settings`); after != before {
		t.Errorf("settings changed:\n%s\n%s", before, after)
	}
	res := r.res.SEO
	if res == nil || len(res.Applied) != 0 || len(res.Would) != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, row := range res.Would {
		if row.Key == "separator" {
			t.Errorf("unchanged separator listed: %+v", row)
		}
		if row.Key == "verification.bing" && (row.Current != "owner-bing" || row.Value != "other-bing") {
			t.Errorf("bing row = %+v", row)
		}
	}
}

// The settings phase fills the company name with the WordPress title; the
// organization name from the SEO plugin must still replace it.
func TestSEOImportOrganizationNameReplacesImportedTitle_Integration(t *testing.T) {
	d, _ := newSEODeps(t)
	ctx := context.Background()
	f := &SEOFile{Plugin: "rankmath", Values: map[string]any{"knowledgegraph_name": "Example Inc"}}
	r := seoRunner(t, d, "http://127.0.0.1:1", ModeEmpty, Options{NoSEOLive: true, SEOFile: f})
	r.dump.Site.Title = "Example Blog"
	if err := r.importSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.importSEO(ctx); err != nil {
		t.Fatal(err)
	}
	cur, err := d.Site.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur.CompanyName != "Example Inc" {
		t.Errorf("company name = %q", cur.CompanyName)
	}
	var found bool
	for _, row := range r.res.SEO.Applied {
		found = found || row.Key == "kg.name"
	}
	if !found {
		t.Errorf("no applied kg.name row: %+v", r.res.SEO)
	}
}
