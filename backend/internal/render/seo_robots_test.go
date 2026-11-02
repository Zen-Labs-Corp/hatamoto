package render

import (
	"context"
	"html/template"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func intPtr(n int) *int { return &n }

func TestHeadRobotsComposition(t *testing.T) {
	t.Parallel()
	settings := func(mut func(*seo.Settings)) *seo.Settings {
		s := seo.Defaults()
		mut(&s)
		return &s
	}
	cases := []struct {
		name string
		view PageView
		want string
	}{
		{"nothing saved", PageView{Kind: KindSingle, SchemaSlug: "post"}, ""},
		{"nothing saved search", PageView{Kind: KindSearch, Page: PageMeta{NoIndex: true}}, "noindex,follow"},
		{"zero settings", PageView{Kind: KindSingle, SchemaSlug: "post", SEO: settings(func(*seo.Settings) {})}, ""},
		{"legacy noindex", PageView{Page: PageMeta{NoIndex: true, RobotsRaw: "noindex, follow"}}, "noindex,follow"},
		{"entry extras", PageView{Page: PageMeta{RobotsRaw: "nofollow, max-snippet:50"}}, "index,nofollow,max-snippet:50"},
		{"type noindex", PageView{Kind: KindSingle, SchemaSlug: "release", SEO: settings(func(s *seo.Settings) {
			s.Index.NoIndexTypes = map[string]bool{"release": true}
		})}, "noindex,follow"},
		{"other type unaffected", PageView{Kind: KindSingle, SchemaSlug: "post", SEO: settings(func(s *seo.Settings) {
			s.Index.NoIndexTypes = map[string]bool{"release": true}
		})}, ""},
		{"taxonomy switch", PageView{Kind: KindTaxonomy, Term: &TermView{Kind: "tag"}, SEO: settings(func(s *seo.Settings) {
			s.Index.NoIndexTaxonomy = map[string]bool{"tag": true}
		})}, "noindex,follow"},
		{"global defaults", PageView{Kind: KindSingle, SEO: settings(func(s *seo.Settings) {
			s.Robots = seo.Robots{MaxSnippet: intPtr(-1), NoArchive: true}
		})}, "index,noarchive,max-snippet:-1"},
		{"indexed search", PageView{Kind: KindSearch, SEO: settings(func(s *seo.Settings) { s.Index.IndexSearch = true })}, ""},
		{"preview wins", PageView{Preview: true, Page: PageMeta{RobotsRaw: "nofollow"}}, "noindex,follow"},
	}
	for _, c := range cases {
		v := c.view
		if got := seoHead(&v, false).Robots; got != c.want {
			t.Errorf("%s: robots %q, want %q", c.name, got, c.want)
		}
	}
}

func TestVerificationMeta(t *testing.T) {
	t.Parallel()
	v := &PageView{BaseURL: "https://x.io/", Path: "/"}
	if h := buildHeadView(context.Background(), v, nil); len(h.Meta) != 0 {
		t.Fatalf("nothing saved must add no meta: %v", h.Meta)
	}
	s := seo.Defaults()
	s.Verification = seo.Verification{Google: `a"b`, Custom: []seo.MetaTag{{Name: "x-check", Content: "1"}}}
	v.SEO = &s
	h := buildHeadView(context.Background(), v, nil)
	if len(h.Meta) != 2 || h.Meta[0] != (metaPair{"google-site-verification", `a"b`}) || h.Meta[1] != (metaPair{"x-check", "1"}) {
		t.Fatalf("meta = %v", h.Meta)
	}
	var sb strings.Builder
	tpl := template.Must(template.New("m").Parse(`{{range .}}<meta name="{{.Key}}" content="{{.Content}}">{{end}}`))
	if err := tpl.Execute(&sb, h.Meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `<meta name="google-site-verification" content="a&#34;b">`) {
		t.Fatalf("not escaped: %s", sb.String())
	}
}

func TestErrorPagesStayPlainNoIndex(t *testing.T) {
	t.Parallel()
	s := seo.Defaults()
	s.Robots = seo.Robots{NoArchive: true}
	v := PageView{SEO: &s, Page: PageMeta{RobotsRaw: "nofollow, max-snippet:5"}}
	nv := v
	nv.Page.NoIndex, nv.Page.RobotsRaw = true, ""
	if got := seoHead(&nv, false).Robots; got != "noindex,follow" {
		t.Fatalf("error page robots %q", got)
	}
}
