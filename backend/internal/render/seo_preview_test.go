package render

import (
	"context"
	"html"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

var (
	headTitleRe = regexp.MustCompile(`<title>(.*?)</title>`)
	headDescRe  = regexp.MustCompile(`<meta name="description" content="(.*?)">`)
	headCanonRe = regexp.MustCompile(`<link rel="canonical" href="(.*?)">`)
	headRobots  = regexp.MustCompile(`<meta name="robots" content="(.*?)">`)
	headOGRe    = regexp.MustCompile(`<meta property="((?:og|article|fb):[^"]*)" content="(.*?)">`)
	headTwRe    = regexp.MustCompile(`<meta name="(twitter:[^"]*)" content="(.*?)">`)
)

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

func allMetas(re *regexp.Regexp, s string) []SEOMeta {
	out := []SEOMeta{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, SEOMeta{Key: m[1], Content: html.UnescapeString(m[2])})
	}
	return out
}

func TestSEOPreviewMatchesHead(t *testing.T) {
	t.Parallel()
	social := func(mut func(*seo.Social)) *seo.Settings { return socialSettings(mut) }
	def := seo.Defaults()
	templated := seo.Defaults()
	templated.Social.Image = "/site.png"
	templated.Social.SiteTags = true

	cases := map[string]*PageView{
		"home": {Kind: KindHome, Site: SiteView{Name: "Site"}, Page: PageMeta{Name: "Home"},
			BaseURL: "https://x.io", Path: "/", SEO: &def},
		"doc with template and social": seoView(PageMeta{Name: "Install", Title: "Install", Description: "How to"}, &templated),
		"noindex type":                 seoView(PageMeta{Name: "Hidden", NoIndex: true}, &def),
		"hand-set meta title": seoView(PageMeta{Name: "N", Title: "Exact", TitleSet: true,
			Description: "d", DescriptionSet: true}, &def),
		"og image and robots": seoView(PageMeta{Name: "N", OGImage: "/own.png", OGTitle: "Own <title>",
			RobotsRaw: "nofollow"}, social(func(s *seo.Social) { s.Image = "/site.png" })),
		"no settings": seoView(PageMeta{Name: "Plain", Title: "Plain"}, nil),
		"preview draft": func() *PageView {
			v := seoView(PageMeta{Name: "Draft"}, &def)
			v.Preview = true
			return v
		}(),
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			var sb strings.Builder
			if err := e.RenderPage(&sb, v); err != nil {
				t.Fatal(err)
			}
			out := sb.String()
			before := *v
			got := SEOPreview(context.Background(), v)
			if !reflect.DeepEqual(before, *v) {
				t.Error("SEOPreview mutated the view")
			}
			want := SEOPreviewTags{
				Title:       firstGroup(headTitleRe, out),
				Description: firstGroup(headDescRe, out),
				Canonical:   firstGroup(headCanonRe, out),
				Robots:      firstGroup(headRobots, out),
				OG:          allMetas(headOGRe, out),
				Twitter:     allMetas(headTwRe, out),
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("preview != rendered head\n got: %+v\nwant: %+v", got, want)
			}
			if got.Title == "" || got.Canonical == "" || len(got.OG) == 0 || len(got.Twitter) == 0 {
				t.Errorf("preview unexpectedly empty: %+v", got)
			}
		})
	}
}
