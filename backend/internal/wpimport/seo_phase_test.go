package wpimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func seoTestRunner(t *testing.T, siteURL string, opts Options) *Runner {
	t.Helper()
	opts.AllowPrivate = true
	c, err := NewClient(ClientOptions{URL: siteURL, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(siteURL)
	return &Runner{opts: opts, dump: &Dump{}, res: &Result{}, client: c, schemaOf: map[string]string{"post": "post"},
		hosts: map[string]bool{strings.ToLower(u.Hostname()): true}, state: &SiteState{Mode: ModeEmpty}}
}

func TestSEOPhaseNothingToDo(t *testing.T) {
	r := seoTestRunner(t, "http://127.0.0.1:1", Options{NoSEOLive: true})
	if err := r.importSEO(context.Background()); err != nil || r.res.SEO != nil || len(r.res.Warnings) != 0 {
		t.Errorf("err=%v seo=%+v warnings=%v", err, r.res.SEO, r.res.Warnings)
	}
}

func TestSEOPhaseDeadHostWarnsAndFinishes(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	r := seoTestRunner(t, url, Options{})
	if err := r.importSEO(context.Background()); err != nil {
		t.Fatalf("importSEO: %v", err)
	}
	if len(r.res.Warnings) == 0 || !strings.Contains(r.res.Warnings[0], "SEO live read") {
		t.Errorf("warnings = %v", r.res.Warnings)
	}
	if r.res.SEO != nil {
		t.Errorf("nothing was read, want no SEO card: %+v", r.res.SEO)
	}
}

func TestSEOPhaseLiveReadsOnlyTheSiteHost(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		http.NotFound(w, req)
	}))
	defer srv.Close()
	r := seoTestRunner(t, srv.URL, Options{})
	r.dump.Items = []Item{{Type: "post", Status: wpPublish, Link: "http://elsewhere.example.com/p/"}, {Type: "post", Status: wpPublish, Link: srv.URL + "/p/"}}
	r.dump.Terms = []Term{{Taxonomy: "category", Link: "http://elsewhere.example.com/c/"}}
	_ = r.importSEO(context.Background())
	for _, p := range paths {
		if p != "/" && p != "/p/" && p != "/robots.txt" {
			t.Errorf("unexpected request %q", p)
		}
	}
	post, archive := r.sampleURLs()
	if post != srv.URL+"/p/" || archive != "" {
		t.Errorf("samples = %q %q", post, archive)
	}
}

func TestSEORowsOnlyListDifferences(t *testing.T) {
	cur := seo.Defaults()
	cur.Separator = "–"
	plan := buildSEOPlan(&SEOFile{Plugin: "rankmath", Values: map[string]any{
		"title_separator": "–", "google_verify": "code",
	}}, nil, testSchemaOf, "")
	kept, _ := pruneSEOPlan(cur, plan)
	applied, would := seoRows(cur, kept)
	if len(applied) != 2 {
		t.Errorf("applied = %+v", applied)
	}
	if len(would) != 1 || would[0].Key != "verification.google" || would[0].Current != "" || would[0].Value != "code" {
		t.Errorf("would = %+v", would)
	}
}

func TestSEOPhaseSourceUploadsAreNotCopied(t *testing.T) {
	r := seoTestRunner(t, "http://127.0.0.1:1", Options{NoSEOLive: true, SEOFile: &SEOFile{Plugin: "rankmath", Values: map[string]any{
		"open_graph_image":    "http://127.0.0.1:1/wp-content/uploads/2024/og.png",
		"knowledgegraph_logo": "http://127.0.0.1:1/wp-content/uploads/logo.png",
	}}})
	if err := r.importSEO(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := r.res.SEO
	if res == nil || len(res.Applied) != 0 || !anyHas(res.NotMapped, "Default share image: upload it") || !anyHas(res.NotMapped, "Organization logo: upload it") {
		t.Errorf("seo = %+v", res)
	}
}

func TestSEOPhaseIsSourceUpload(t *testing.T) {
	r := seoTestRunner(t, "http://127.0.0.1:1", Options{})
	for raw, want := range map[string]bool{
		"http://127.0.0.1:1/wp-content/uploads/a.png":      true,
		"http://127.0.0.1:1/images/a.png":                  false,
		"https://cdn.example.com/wp-content/uploads/a.png": false,
	} {
		if got := r.isSourceUpload(raw); got != want {
			t.Errorf("%s = %v", raw, got)
		}
	}
}
