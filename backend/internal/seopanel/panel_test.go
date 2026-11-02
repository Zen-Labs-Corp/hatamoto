package seopanel

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

func TestSEOPanelScore(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		p    Panel
		want int
	}{
		"no checks":  {Panel{}, 0},
		"all pass":   {Panel{Pass: 4, Total: 4}, 100},
		"all fail":   {Panel{Fail: 4, Total: 4}, 0},
		"half":       {Panel{Pass: 1, Fail: 1, Total: 2}, 50},
		"warn half":  {Panel{Warn: 2, Total: 2}, 50},
		"rounds up":  {Panel{Pass: 1, Warn: 1, Fail: 1, Total: 3}, 50},
		"rounds 2/3": {Panel{Pass: 2, Fail: 1, Total: 3}, 67},
	} {
		if got := tc.p.Score(); got != tc.want {
			t.Errorf("%s: Score() = %d, want %d", name, got, tc.want)
		}
	}
}

func TestSEOPanelScoreFollowsTheChecks(t *testing.T) {
	t.Parallel()

	p := Build(Input{Title: "Go", Slug: "go", Content: "<p>short</p>"})
	if want := (200*p.Pass + 100*p.Warn + p.Total) / (2 * p.Total); p.Score() != want || p.Score() < 0 || p.Score() > 100 {
		t.Errorf("Score() = %d from %d/%d/%d of %d", p.Score(), p.Pass, p.Warn, p.Fail, p.Total)
	}
	if again := Build(Input{Title: "Go", Slug: "go", Content: "<p>short</p>"}); again.Score() != p.Score() {
		t.Error("the score is not deterministic")
	}
}

func TestSEOPanelMetaOnlySkipsBodyChecks(t *testing.T) {
	t.Parallel()

	in := Input{Title: "Go servers in production: a guide", Slug: "go-servers", Keyword: "Go servers", Content: "<h2>Go servers</h2><p>Go servers are fast.</p>"}
	full := Build(in)
	in.Mode = ModeMetaOnly
	meta := Build(in)

	if len(meta.Answer) != 0 || len(meta.SEO) >= len(full.SEO) {
		t.Fatalf("meta-only kept body checks: %d answer, %d search (full: %d)", len(meta.Answer), len(meta.SEO), len(full.SEO))
	}
	for _, c := range meta.SEO {
		switch c.ID {
		case "keyword-title", "keyword-description", "keyword-slug":
		default:
			t.Errorf("meta-only ran the %q check", c.ID)
		}
	}
	if meta.Total != 2+len(meta.SEO) {
		t.Errorf("Total = %d, want %d", meta.Total, 2+len(meta.SEO))
	}
}

func TestSEOPanelPreviewUsesTheResolver(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	set.Types = map[string]seo.TypeTemplates{"doc": {Title: "%title% %sep% Docs", Description: "About %title%."}}
	in := Input{Title: "Install", Slug: "install", SEO: &set, SchemaSlug: "doc", SiteName: "Example"}
	p := Build(in)

	want := seo.Resolve(&set, in.ResolverInput())
	if p.SERP.Title != want.Title || p.SERP.Title != "Install | Docs" {
		t.Errorf("preview title = %q, resolver = %q", p.SERP.Title, want.Title)
	}
	if p.SERP.Description != "About Install." {
		t.Errorf("preview description = %q, want the type template", p.SERP.Description)
	}

	in.MetaTitle, in.MetaDescription = "Hand written", "My own."
	p = Build(in)
	if p.SERP.Title != "Hand written | Example" || p.SERP.Description != "My own." {
		t.Errorf("hand-written values = %q / %q", p.SERP.Title, p.SERP.Description)
	}
}

func TestSEOPanelChecksMeasureTheTypedTitle(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	typed := strings.Repeat("a", 50)
	in := Input{Title: typed, Slug: "x", SEO: &set, SchemaSlug: "doc", SiteName: "A Rather Long Site Name", Keyword: "Rather"}
	p := Build(in)
	if p.SERP.TitleLen != 50 || p.SERP.TitleStatus != CheckPass {
		t.Errorf("title length = %d (%s), want 50 pass", p.SERP.TitleLen, p.SERP.TitleStatus)
	}
	if want := typed + " | A Rather Long Site Name"; p.SERP.Title != want {
		t.Errorf("preview title = %q, want %q", p.SERP.Title, want)
	}
	for _, c := range p.SEO {
		if c.ID == "keyword-title" && c.Status != CheckFail {
			t.Errorf("keyword in the site name only passed the title check: %+v", c)
		}
	}
}

func TestSEOPanelCheckCopyIsKindNeutral(t *testing.T) {
	t.Parallel()

	for _, content := range []string{"<p>Short.</p>", strings.Repeat("<p>word </p>", 5)} {
		p := Build(Input{Title: "About", Slug: "about", Content: content, SchemaSlug: "page", BaseURL: "https://example.test", Path: "/about", InLLMs: true})
		for _, c := range append(append([]Check{}, p.SEO...), p.Answer...) {
			if strings.Contains(strings.ToLower(c.Message), "post") {
				t.Errorf("check %q says post: %q", c.ID, c.Message)
			}
		}
	}
}

func TestSEOPanelPreviewKinds(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	set.Home = "Home: %sitename% - %tagline%"
	set.Taxonomy = map[string]string{"category": "Category %term% %sep% %sitename%", "archive": "Archive %archive% %sep% %sitename%"}
	base := Input{Title: "Welcome", Slug: "welcome", SEO: &set, SiteName: "Example", Tagline: "Fast"}

	home := base
	home.Home = true
	if got := Build(home).SERP.Title; got != "Home: Example - Fast" {
		t.Errorf("home preview = %q, want the Home template", got)
	}

	term := base
	term.Kind, term.TermKind, term.Term = seo.KindTaxonomy, "category", "Guides"
	if got := Build(term).SERP.Title; got != "Category Guides | Example" {
		t.Errorf("term preview = %q, want the taxonomy template", got)
	}

	arch := term
	arch.TermKind = "archive"
	if got := Build(arch).SERP.Title; got != "Archive Guides | Example" {
		t.Errorf("%%archive%% preview = %q, want the term name", got)
	}
	if got := term.ResolverInput().Vars.Archive; got != "Guides" {
		t.Errorf("Vars.Archive = %q, want Guides", got)
	}
	if got := base.ResolverInput().Vars.Archive; got != "" {
		t.Errorf("single Vars.Archive = %q, want empty", got)
	}

	if got := base.ResolverInput().Kind; got != seo.KindSingle {
		t.Errorf("default kind = %q, want single", got)
	}
}

func TestSEOPanelShareCard(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	set.Social.Image = "/media/site.png"
	set.Social.Types = map[string]seo.SocialType{"doc": {Title: "%title% %sep% Docs", Image: "/media/docs.png"}}
	base := Input{Title: "Guide", MetaDescription: "Meta words.", BaseURL: "https://example.test", Slug: "guide", Path: "/guide", Social: true, SEO: &set, SchemaSlug: "doc", SiteName: "Example"}

	c := Build(base).Card
	if !c.Show || c.Title != "Guide | Docs" || c.Description != "Meta words." || c.Image != "https://example.test/media/docs.png" || c.Site != "Example" {
		t.Errorf("template card = %+v", c)
	}

	hand := base
	hand.OGTitle, hand.OGDescription, hand.OGImage = " Mine ", "Mine too", "https://cdn.example.test/e.png"
	if c := Build(hand).Card; c.Title != "Mine" || c.Description != "Mine too" || c.Image != "https://cdn.example.test/e.png" {
		t.Errorf("hand-written card = %+v", c)
	}

	site := base
	site.SchemaSlug = "page"
	if c := Build(site).Card; c.Title != "Guide" || c.Image != "https://example.test/media/site.png" {
		t.Errorf("site default card = %+v", c)
	}

	term := base
	term.Social = false
	if Build(term).Card.Show {
		t.Error("a form without social fields gets no card")
	}
}

func TestFromAttrs(t *testing.T) {
	t.Parallel()

	got := FromAttrs(map[string]any{
		"meta_title": "T", "meta_description": "D", "canonical_url": "https://example.test/x", "og_image": "/i.png",
		"og_title": "OT", "og_description": "OD", "schema_type": "Course", "custom_jsonld": "{}",
		"hide_from_sitemap": "true", "breadcrumb_title": "B", "primary_term": "t1",
		"seo": map[string]any{"robots": "noindex, nofollow, noarchive, nosnippet, max-snippet:40, max-image-preview:large"},
	})
	want := Attrs{Title: "T", Description: "D", Canonical: "https://example.test/x", OGImage: "/i.png", OGTitle: "OT", OGDescription: "OD",
		NoIndex: true, SchemaType: "Course", CustomJSONLD: "{}", HideFromSitemap: true, BreadcrumbTitle: "B", PrimaryTerm: "t1",
		NoFollow: true, NoArchive: true, NoSnippet: true, MaxSnippet: "40", MaxImagePreview: "large"}
	if got != want {
		t.Errorf("FromAttrs = %+v, want %+v", got, want)
	}
	if !FromAttrs(map[string]any{"no_index": true}).NoIndex || FromAttrs(map[string]any{}).NoIndex {
		t.Error("no_index is the only other way to ask for noindex")
	}
}

func TestBodyProperty(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ schema, want string }{
		"content":  {`{"properties":{"content":{"type":"string"}}}`, "content"},
		"body":     {`{"properties":{"body":{"type":"string"}}}`, "body"},
		"both":     {`{"properties":{"body":{"type":"string"},"content":{"type":"string"}}}`, "content"},
		"not text": {`{"properties":{"content":{"type":"object"}}}`, ""},
		"none":     {`{"type":"object"}`, ""},
		"invalid":  {`{`, ""},
	} {
		if got := BodyProperty(&entity.EntitySchema{Schema: []byte(tc.schema)}); got != tc.want {
			t.Errorf("%s: BodyProperty = %q, want %q", name, got, tc.want)
		}
	}
}
