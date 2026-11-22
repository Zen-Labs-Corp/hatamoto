package staticexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/redirect"
)

type fakeResp struct {
	status int
	ct     string
	loc    string
	body   string
	// failFirst answers 429 for the first n calls.
	failFirst int
}

type fakeSource struct {
	site   Site
	seeds  []string
	rules  []*redirect.Redirect
	pages  map[string]fakeResp
	mu     sync.Mutex
	calls  []string
	counts map[string]int
}

func newFake(admin string, seeds ...string) *fakeSource {
	base, _ := url.Parse("https://example.com")
	return &fakeSource{
		site:   Site{Base: base, AdminPrefix: admin},
		seeds:  seeds,
		pages:  map[string]fakeResp{},
		counts: map[string]int{},
	}
}

func (f *fakeSource) html(p, body string) {
	f.pages[p] = fakeResp{status: 200, ct: "text/html; charset=utf-8", body: body}
}
func (f *fakeSource) put(p, ct, body string) {
	f.pages[p] = fakeResp{status: 200, ct: ct, body: body}
}

func (f *fakeSource) Site(context.Context) (Site, error)      { return f.site, nil }
func (f *fakeSource) Seeds(context.Context) ([]string, error) { return f.seeds, nil }
func (f *fakeSource) Redirects(context.Context) ([]*redirect.Redirect, error) {
	return f.rules, nil
}

func (f *fakeSource) Fetch(_ context.Context, p string, w io.Writer) (Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, p)
	f.counts[p]++
	n := f.counts[p]
	r, ok := f.pages[p]
	f.mu.Unlock()
	if !ok {
		r = fakeResp{status: 404, ct: "text/html; charset=utf-8", body: "<h1>Not found</h1>"}
	}
	h := http.Header{}
	if r.ct != "" {
		h.Set("Content-Type", r.ct)
	}
	if r.loc != "" {
		h.Set("Location", r.loc)
	}
	if n <= r.failFirst {
		h.Set("Retry-After", "1")
		return Response{Status: 429, Header: h}, nil
	}
	io.WriteString(w, r.body)
	return Response{Status: r.status, Header: h}, nil
}

func (f *fakeSource) called(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == p {
			return true
		}
	}
	return false
}

func build(t *testing.T, f *fakeSource) (Manifest, Report, string, error) {
	t.Helper()
	dir := t.TempDir()
	m, r, err := Build(context.Background(), f, f.site, dir, BuildOptions{})
	return m, r, dir, err
}

func mustHave(t *testing.T, m Manifest, files ...string) {
	t.Helper()
	for _, p := range files {
		if _, ok := m.Files[p]; !ok {
			t.Errorf("manifest misses %s (has %v)", p, keys(m))
		}
	}
}

func keys(m Manifest) []string {
	var out []string
	for k := range m.Files {
		out = append(out, k)
	}
	return out
}

func TestBuild_InventoryUnion(t *testing.T) {
	font := "/fonts/" + strings.Repeat("ab", 32) + ".woff2"
	f := newFake("/k3x9-admin", "/", "/sitemap.xml", "/llms.txt", "/llms-full.txt")
	f.html("/", `<a href="/category/news">n</a><link rel="stylesheet" href="/theme/assets/theme.abc123.css">
<img src="/media/a.jpg" srcset="/media/a-300.webp 300w, /media/a.jpg 1200w">`)
	f.put("/sitemap.xml", "application/xml", `<sitemapindex><loc>https://example.com/sitemap-pages.xml</loc></sitemapindex>`)
	f.put("/sitemap-pages.xml", "application/xml", `<urlset><loc>https://example.com/about</loc></urlset>`)
	f.html("/about", "<p>about</p>")
	f.html("/category/news", "<p>news</p>")
	f.put("/theme/assets/theme.abc123.css", "text/css", `@font-face{src:url("`+font+`")}`)
	f.put(font, "font/woff2", "FONT")
	f.put("/media/a.jpg", "image/jpeg", "JPG")
	f.put("/media/a-300.webp", "image/webp", "WEBP")
	f.put("/llms.txt", "text/plain; charset=utf-8", "https://example.com/api/v1/public/knowledge-graph\nhttps://example.com/api/v1/public/knowledge-graph?schema=post\n")
	f.put("/llms-full.txt", "text/plain; charset=utf-8", "# Acme Studio: full text\n")
	f.put("/api/v1/public/knowledge-graph", "application/ld+json", `{"@graph":[]}`)

	m, rep, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "index.html", "sitemap.xml", "sitemap-pages.xml", "about/index.html", "category/news/index.html",
		"theme/assets/theme.abc123.css", font[1:], "media/a.jpg", "media/a-300.webp", "llms.txt", "llms-full.txt", "404.html",
		"api/v1/public/knowledge-graph")
	if got := m.Files["api/v1/public/knowledge-graph"].Type; got != "application/ld+json" {
		t.Errorf("knowledge graph type = %q", got)
	}
	if rep.Skipped != 1 || f.called("/api/v1/public/knowledge-graph?schema=post") {
		t.Errorf("skipped = %d, schema URL fetched = %v", rep.Skipped, f.called("/api/v1/public/knowledge-graph?schema=post"))
	}
	if rep.Media != 2 || rep.Pages != 3 {
		t.Errorf("pages=%d media=%d, want 3 and 2", rep.Pages, rep.Media)
	}
	if _, err := os.Stat(filepath.Join(dir, stagingTmp)); !os.IsNotExist(err) {
		t.Errorf("staging temp dir left behind: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "about", "index.html")); string(b) != "<p>about</p>" {
		t.Errorf("about body = %q", b)
	}
}

func TestBuild_AdsOnlyWhenNonEmpty(t *testing.T) {
	f := newFake("/admin", "/", "/ads.txt")
	f.html("/", "<p>home</p>")
	m, _, _, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files["ads.txt"]; ok {
		t.Error("ads.txt present although the site answers 404")
	}
	f.put("/ads.txt", "text/plain; charset=utf-8", "google.com, pub-1, DIRECT\n")
	m, _, _, err = build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "ads.txt")
}

// Switching an AI file off makes it answer 404; the export still succeeds.
func TestBuild_AIFilesSwitchedOffDoNotFailTheExport(t *testing.T) {
	f := newFake("/admin", "/", "/llms.txt", "/llms-full.txt")
	f.html("/", "<p>home</p>")
	m, _, _, err := build(t, f)
	if err != nil {
		t.Fatalf("export failed with both files off: %v", err)
	}
	for _, name := range []string{"llms.txt", "llms-full.txt"} {
		if _, ok := m.Files[name]; ok {
			t.Errorf("%s present although the site answers 404", name)
		}
	}
	f.put("/llms-full.txt", "text/plain; charset=utf-8", "# Acme Studio: full text\n")
	m, _, _, err = build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "llms-full.txt")
	if _, ok := m.Files["llms.txt"]; ok {
		t.Error("llms.txt present although it is switched off")
	}
}

func TestBuild_ExcludesAdminAndPrivate(t *testing.T) {
	f := newFake("/admin", "/")
	f.html("/", `<a href="/admin/login">a</a><a href="/api/v1/public/track">t</a><a href="/preview/t">p</a>
<a href="/auth/login">x</a><a href="/p/forms/submit">f</a>`)
	if _, _, _, err := build(t, f); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/admin/login", "/api/v1/public/track", "/preview/t", "/auth/login", "/p/forms/submit"} {
		if f.called(p) {
			t.Errorf("%s was fetched", p)
		}
	}
}

func TestBuild_ListingPages(t *testing.T) {
	f := newFake("/admin", "/blog")
	f.html("/blog", `<a href="/blog?page=2">next</a><link rel="next" href="https://example.com/blog?page=2">`)
	f.html("/blog?page=2", `<a href="?page=3">n</a><a href="/blog">first</a>`)
	f.html("/blog?page=3", `<link rel="canonical" href="https://example.com/blog?page=3"><a href="/blog?page=2">p</a>`)
	m, rep, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "blog/index.html", "blog/page/2/index.html", "blog/page/3/index.html")
	if rep.ListingPages != 2 || rep.Skipped != 0 || rep.Pages != 1 {
		t.Errorf("listing=%d skipped=%d pages=%d (warnings %v)", rep.ListingPages, rep.Skipped, rep.Pages, rep.Warnings)
	}
	if strings.Contains(strings.Join(rep.Warnings, "\n"), "?page=N") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if !f.called("/blog?page=2") {
		t.Error("the query did not reach the source")
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("blog/index.html"); !strings.Contains(got, `href="/blog/page/2/"`) || !strings.Contains(got, `href="https://example.com/blog/page/2/"`) {
		t.Errorf("blog/index.html = %q", got)
	}
	if got := read("blog/page/2/index.html"); !strings.Contains(got, `href="/blog/page/3/"`) {
		t.Errorf("page 2 = %q", got)
	}
	if got := read("blog/page/3/index.html"); !strings.Contains(got, `rel="canonical" href="https://example.com/blog?page=3"`) {
		t.Errorf("page 3 lost its canonical: %q", got)
	}
	for p, e := range m.Files {
		sum := sha256.Sum256([]byte(read(p)))
		if hex.EncodeToString(sum[:]) != e.SHA256 || e.Size != int64(len(read(p))) {
			t.Errorf("%s: manifest does not match the staged bytes", p)
		}
	}
}

func TestBuild_ListingCollision(t *testing.T) {
	f := newFake("/admin", "/blog", "/blog/page/2")
	f.html("/blog", `<a href="/blog?page=2">next</a>`)
	f.html("/blog?page=2", "<p>listing</p>")
	f.html("/blog/page/2", "<p>entity</p>")
	m, rep, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "blog", "page", "2", "index.html")); string(b) != "<p>entity</p>" {
		t.Errorf("file = %q", b)
	}
	if rep.ListingPages != 0 || len(rep.Warnings) == 0 {
		t.Errorf("listing=%d warnings=%v", rep.ListingPages, rep.Warnings)
	}
	if e := m.Files["blog/page/2/index.html"]; e.Size != int64(len("<p>entity</p>")) {
		t.Errorf("entry = %+v", e)
	}
}

func TestBuild_ListingRedirectNoStub(t *testing.T) {
	f := newFake("/admin", "/blog")
	f.html("/blog", `<a href="/blog?page=2">next</a>`)
	f.pages["/blog?page=2"] = fakeResp{status: 301, loc: "/blog"}
	m, rep, _, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stubs != 0 || rep.ListingPages != 0 || len(m.Files) != 2 {
		t.Errorf("stubs=%d listing=%d files=%v", rep.Stubs, rep.ListingPages, keys(m))
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "redirects (301)") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
}

func TestBuild_ListingPageOneIsListing(t *testing.T) {
	f := newFake("/admin", "/blog")
	f.html("/blog", `<a href="/blog?page=1">first</a>`)
	m, rep, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if f.called("/blog?page=1") || rep.Skipped != 0 || len(rep.Warnings) != 0 || rep.ListingPages != 0 {
		t.Errorf("fetched=%v skipped=%d warnings=%v", f.called("/blog?page=1"), rep.Skipped, rep.Warnings)
	}
	mustHave(t, m, "blog/index.html")
	if b, _ := os.ReadFile(filepath.Join(dir, "blog", "index.html")); string(b) != `<a href="/blog">first</a>` {
		t.Errorf("body = %q", b)
	}
}

func TestBuild_AssetCacheBusterExportedByPath(t *testing.T) {
	f := newFake("/admin", "/")
	f.html("/", `<script src="/theme/assets/consent.js?v=abc"></script><link href="/fonts/a.woff2?v=1"><a href="/blog?page=2">n</a>`)
	f.put("/theme/assets/consent.js", "application/javascript", "console.log(1)")
	f.put("/fonts/a.woff2", "font/woff2", "font")
	m, rep, _, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "theme/assets/consent.js", "fonts/a.woff2")
	if rep.Skipped != 0 || !f.called("/blog?page=2") {
		t.Errorf("skipped = %d, ?page=2 fetched = %v (warnings %v)", rep.Skipped, f.called("/blog?page=2"), rep.Warnings)
	}
}

func TestBuild_RedirectStubsAndLines(t *testing.T) {
	f := newFake("/admin", "/")
	f.html("/", `<a href="/old">o</a>`)
	f.pages["/old"] = fakeResp{status: 301, loc: "/new"}
	f.pages["/dbold"] = fakeResp{status: 301, loc: "https://example.com/new"}
	f.html("/new", "<p>new</p>")
	f.rules = []*redirect.Redirect{
		{SourcePath: "/legacy/*", TargetURL: "/blog/$1", StatusCode: 301},
		{SourcePath: "/dbold", TargetURL: "/new", StatusCode: 301},
	}
	m, rep, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "old/index.html", "dbold/index.html", "new/index.html", RedirectsFile)
	stub, _ := os.ReadFile(filepath.Join(dir, "old", "index.html"))
	if !strings.Contains(string(stub), `url=https://example.com/new"`) {
		t.Errorf("stub = %s", stub)
	}
	got, _ := os.ReadFile(filepath.Join(dir, RedirectsFile))
	want := "/dbold /new 301\n/old /new 301\n/legacy/:p1 /blog/:p1 301\n"
	if string(got) != want {
		t.Errorf("_redirects:\n%s\nwant:\n%s", got, want)
	}
	if rep.Stubs != 2 || rep.Redirects != 3 {
		t.Errorf("stubs=%d redirects=%d", rep.Stubs, rep.Redirects)
	}
}

func TestBuild_RenderErrorFails(t *testing.T) {
	f := newFake("/admin", "/")
	f.html("/", `<a href="/blog/x">x</a>`)
	f.pages["/blog/x"] = fakeResp{status: 500, ct: "text/html", body: "boom"}
	_, rep, _, err := build(t, f)
	if err == nil || !strings.Contains(err.Error(), "/blog/x: render 500") {
		t.Fatalf("err = %v", err)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("errors = %v", rep.Errors)
	}
}

func TestBuild_SitemapChild429Retries(t *testing.T) {
	old := retrySleep
	retrySleep = func(context.Context, time.Duration) {}
	defer func() { retrySleep = old }()

	f := newFake("/admin", "/", "/sitemap-pages.xml")
	f.html("/", "<p>home</p>")
	f.pages["/sitemap-pages.xml"] = fakeResp{status: 200, ct: "application/xml", body: "<urlset/>", failFirst: 2}
	m, _, _, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "sitemap-pages.xml")
	if f.counts["/sitemap-pages.xml"] != 3 {
		t.Errorf("calls = %d, want 3", f.counts["/sitemap-pages.xml"])
	}
}

func TestBuild_AdminAddressInBodyFails(t *testing.T) {
	f := newFake("/k3x9-admin", "/")
	f.html("/", `<a href="/k3x9-admin">x</a>`)
	_, _, _, err := build(t, f)
	if err == nil || !strings.Contains(err.Error(), "admin address") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuild_NotFoundPage(t *testing.T) {
	f := newFake("/admin", "/")
	f.html("/", "<p>home</p>")
	m, _, dir, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, m, "404.html")
	if b, _ := os.ReadFile(filepath.Join(dir, "404.html")); !strings.Contains(string(b), "Not found") {
		t.Errorf("404.html = %q", b)
	}

	f.pages["/404.html"] = fakeResp{status: 404, ct: "application/json", body: "{}"}
	if _, _, _, err := build(t, f); err == nil || !strings.Contains(err.Error(), "404 HTML page") {
		t.Fatalf("json 404: err = %v", err)
	}
}

func TestBuild_SiteWarningsReachReport(t *testing.T) {
	f := newFake("/k3x9-admin", "/")
	f.html("/", `<p>hi</p>`)
	f.site.Warnings = []string{"note from the source"}
	_, r, _, err := build(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Warnings, "note from the source") {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

// A listing page and a real page that map to the same file are stored at
// the same time: the file left on disk must be the one the manifest describes.
func TestStore_ListingCollisionKeepsManifestAndFileInStep(t *testing.T) {
	base, _ := url.Parse("https://example.test")
	dir := t.TempDir()
	b := &builder{
		site: Site{Base: base}, dir: dir, tmp: filepath.Join(dir, stagingTmp),
		manifest:   Manifest{Version: manifestVersion, Files: map[string]Entry{}},
		listingOut: map[string]bool{},
	}
	if err := os.MkdirAll(b.tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) (string, string) {
		f := filepath.Join(b.tmp, name)
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(body))
		return f, hex.EncodeToString(h[:])
	}
	lf, ls := put("listing", "<p>listing</p>")
	rf, rs := put("real", "<p>real page</p>")

	// The listing page pauses in its placement until the real page has been
	// stored, or a moment passes when the real page has to wait for it.
	inPlace, realDone := make(chan struct{}), make(chan struct{})
	b.placeFn = func(from, to string) error {
		if from == lf {
			close(inPlace)
			select {
			case <-realDone:
			case <-time.After(300 * time.Millisecond):
			}
		}
		return place(from, to)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.store("/blog", "/blog?page=2", lf, 14, ls, "text/html", "text/html", true, 2)
	}()
	go func() {
		defer wg.Done()
		<-inPlace
		b.store("/blog/page/2", "/blog/page/2", rf, 16, rs, "text/html", "text/html", true, 0)
		close(realDone)
	}()
	wg.Wait()
	if len(b.manifest.Files) != 1 {
		t.Fatalf("manifest = %v, want one file", b.manifest.Files)
	}
	for out, e := range b.manifest.Files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(out)))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(got)
		if hex.EncodeToString(h[:]) != e.SHA256 {
			t.Fatalf("%s holds %q but the manifest sha is %s", out, got, e.SHA256)
		}
	}
}
