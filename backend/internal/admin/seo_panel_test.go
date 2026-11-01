package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/seo"
)

func TestSEOPanelScore(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		p    seoPanel
		want int
	}{
		"no checks":  {seoPanel{}, 0},
		"all pass":   {seoPanel{Pass: 4, Total: 4}, 100},
		"all fail":   {seoPanel{Fail: 4, Total: 4}, 0},
		"half":       {seoPanel{Pass: 1, Fail: 1, Total: 2}, 50},
		"warn half":  {seoPanel{Warn: 2, Total: 2}, 50},
		"rounds up":  {seoPanel{Pass: 1, Warn: 1, Fail: 1, Total: 3}, 50},
		"rounds 2/3": {seoPanel{Pass: 2, Fail: 1, Total: 3}, 67},
	} {
		if got := tc.p.Score(); got != tc.want {
			t.Errorf("%s: Score() = %d, want %d", name, got, tc.want)
		}
	}
}

func TestSEOPanelScoreFollowsTheChecks(t *testing.T) {
	t.Parallel()

	p := buildPanel(seoInput{Title: "Go", Slug: "go", Content: "<p>short</p>"})
	if want := (200*p.Pass + 100*p.Warn + p.Total) / (2 * p.Total); p.Score() != want || p.Score() < 0 || p.Score() > 100 {
		t.Errorf("Score() = %d from %d/%d/%d of %d", p.Score(), p.Pass, p.Warn, p.Fail, p.Total)
	}
	if again := buildPanel(seoInput{Title: "Go", Slug: "go", Content: "<p>short</p>"}); again.Score() != p.Score() {
		t.Error("the score is not deterministic")
	}
}

func TestSEOPanelMetaOnlySkipsBodyChecks(t *testing.T) {
	t.Parallel()

	in := seoInput{Title: "Go servers in production: a guide", Slug: "go-servers", Keyword: "Go servers", Content: "<h2>Go servers</h2><p>Go servers are fast.</p>"}
	full := buildPanel(in)
	in.Mode = ModeMetaOnly
	meta := buildPanel(in)

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
	in := seoInput{Title: "Install", Slug: "install", SEO: &set, SchemaSlug: "doc", SiteName: "Example"}
	p := buildPanel(in)

	want := seo.Resolve(&set, in.resolverInput())
	if p.SERP.Title != want.Title || p.SERP.Title != "Install | Docs" {
		t.Errorf("preview title = %q, resolver = %q", p.SERP.Title, want.Title)
	}
	if p.SERP.Description != "About Install." {
		t.Errorf("preview description = %q, want the type template", p.SERP.Description)
	}

	in.MetaTitle, in.MetaDescription = "Hand written", "My own."
	p = buildPanel(in)
	if p.SERP.Title != "Hand written | Example" || p.SERP.Description != "My own." {
		t.Errorf("hand-written values = %q / %q", p.SERP.Title, p.SERP.Description)
	}
}

func TestPartialSEOPanel(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	panel := buildPanel(seoInput{Title: "Go servers in production", Slug: "go-servers", Keyword: "Go servers", Content: "<p>Go servers are easy to ship.</p>", BaseURL: "https://example.test", Path: "/go-servers"})
	data := map[string]any{
		"Panel": panel, "SEO": seoForm{Title: "Go servers", Canonical: "https://example.test/go", OGTitle: "Share title", OGDescription: "Share text"}, "Errors": map[string]string{},
		"Noun": "post", "AnalyzeURL": "/admin/entities/x/edit/analyze", "Include": "#form",
		"Keyword": "Go servers", "ShowKeyword": true, "InLLMs": true, "ShowLLMs": true, "ShowSocialText": true,
	}
	s := checkPartial(t, a, "hm/seopanel", "default", data)
	for _, want := range []string{
		`data-hm-seo-tabs`, `id="seo-tab-search"`, `id="seo-tab-social"`, `id="seo-tab-schema"`, `id="seo-tab-advanced"`,
		`id="seo-score"`, `name="seo_keyword"`, `name="seo_llms"`, `name="seo_canonical"`, `name="seo_og_image"`,
		`name="seo_og_title"`, `name="seo_og_description"`, `value="Share title"`,
		`hx-post="/admin/entities/x/edit/analyze"`, `id="be-panel"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	data["ShowKeyword"], data["ShowLLMs"] = false, false
	s = checkPartial(t, a, "hm/seopanel", "no-keyword", data)
	if strings.Contains(s, `name="seo_keyword"`) || strings.Contains(s, `name="seo_llms"`) {
		t.Error("the keyword and llms fields show only when asked for")
	}
	delete(data, "ShowSocialText")
	if out, err := a.RenderPartial("hm/seopanel", data); err != nil || strings.Contains(string(out), `name="seo_og_title"`) || strings.Contains(string(out), `name="seo_og_description"`) {
		t.Error("the social text fields show only with ShowSocialText (terms leave it off)")
	}
	data["ShowSocialText"] = true
	data["NoHeading"] = true
	s = checkPartial(t, a, "hm/seopanel", "no-heading", data)
	if strings.Contains(s, "hm-seo__title") || !strings.Contains(s, `id="seo-score"`) {
		t.Error("NoHeading drops the heading and keeps the score")
	}
	frag := checkPartial(t, a, "hm/seopanel-fragment", "default", panel)
	if strings.Count(frag, `hx-swap-oob="true"`) != 5 {
		t.Errorf("the fragment swaps the score, two counters, the structured data and the share card out of band: %s", frag)
	}
}

// With SEO settings present the checks measure the typed title, not the
// suffixed one the preview shows.
func TestSEOPanelChecksMeasureTheTypedTitle(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	typed := strings.Repeat("a", 50)
	in := seoInput{Title: typed, Slug: "x", SEO: &set, SchemaSlug: "doc", SiteName: "A Rather Long Site Name", Keyword: "Rather"}
	p := buildPanel(in)
	if p.SERP.TitleLen != 50 || p.SERP.TitleStatus != checkPass {
		t.Errorf("title length = %d (%s), want 50 pass", p.SERP.TitleLen, p.SERP.TitleStatus)
	}
	if want := typed + " | A Rather Long Site Name"; p.SERP.Title != want {
		t.Errorf("preview title = %q, want %q", p.SERP.Title, want)
	}
	for _, c := range p.SEO {
		if c.ID == "keyword-title" && c.Status != checkFail {
			t.Errorf("keyword in the site name only passed the title check: %+v", c)
		}
	}
}

// The panel also serves pages and custom entity types, so no check message
// may say "post".
func TestSEOPanelCheckCopyIsKindNeutral(t *testing.T) {
	t.Parallel()

	for _, content := range []string{"<p>Short.</p>", strings.Repeat("<p>word </p>", 5)} {
		p := buildPanel(seoInput{Title: "About", Slug: "about", Content: content, SchemaSlug: "page", BaseURL: "https://example.test", Path: "/about", InLLMs: true})
		for _, c := range append(append([]seoCheck{}, p.SEO...), p.Answer...) {
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
	base := seoInput{Title: "Welcome", Slug: "welcome", SEO: &set, SiteName: "Example", Tagline: "Fast"}

	home := base
	home.Home = true
	if got := buildPanel(home).SERP.Title; got != "Home: Example - Fast" {
		t.Errorf("home preview = %q, want the Home template", got)
	}

	term := base
	term.Kind, term.TermKind, term.Term = seo.KindTaxonomy, "category", "Guides"
	if got := buildPanel(term).SERP.Title; got != "Category Guides | Example" {
		t.Errorf("term preview = %q, want the taxonomy template", got)
	}

	arch := term
	arch.TermKind = "archive"
	if got := buildPanel(arch).SERP.Title; got != "Archive Guides | Example" {
		t.Errorf("%%archive%% preview = %q, want the term name", got)
	}
	if got := term.resolverInput().Vars.Archive; got != "Guides" {
		t.Errorf("Vars.Archive = %q, want Guides", got)
	}
	if got := base.resolverInput().Vars.Archive; got != "" {
		t.Errorf("single Vars.Archive = %q, want empty", got)
	}

	if got := base.resolverInput().Kind; got != seo.KindSingle {
		t.Errorf("default kind = %q, want single", got)
	}
}

type fakeHomeExplain struct{ id string }

func (f fakeHomeExplain) ExplainURL(context.Context, string) (URLExplanation, error) {
	return URLExplanation{Page: PageAnswer{Kind: "home", EntityID: f.id}}, nil
}

func TestMarkHome(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id, other := uuid.New(), uuid.New()
	mark := func(a *Admin, e uuid.UUID) bool {
		in := seoInput{}
		a.markHome(ctx, &in, e)
		return in.Home
	}

	if mark(&Admin{}, id) {
		t.Error("no stores: must not be home")
	}

	site := newFakeSite("Example")
	site.cfg.HomepageEntityID = id
	a := &Admin{deps: Deps{Site: site, Growth: GrowthDeps{Explain: fakeHomeExplain{id: other.String()}}}}
	if !mark(a, id) || mark(a, other) {
		t.Error("a chosen homepage decides, whatever the explainer says")
	}
	if mark(a, uuid.Nil) {
		t.Error("a new entry is never the homepage")
	}

	site.cfg.HomepageEntityID = uuid.Nil
	if !mark(a, other) || mark(a, id) {
		t.Error("with none chosen the page served at / is the homepage")
	}
}

func TestSEOPanelShareCard(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	set.Social.Image = "/media/site.png"
	set.Social.Types = map[string]seo.SocialType{"doc": {Title: "%title% %sep% Docs", Image: "/media/docs.png"}}
	base := seoInput{Title: "Guide", MetaDescription: "Meta words.", BaseURL: "https://example.test", Slug: "guide", Path: "/guide", Social: true, SEO: &set, SchemaSlug: "doc", SiteName: "Example"}

	c := buildPanel(base).Card
	if !c.Show || c.Title != "Guide | Docs" || c.Description != "Meta words." || c.Image != "https://example.test/media/docs.png" || c.Site != "Example" {
		t.Errorf("template card = %+v", c)
	}

	hand := base
	hand.OGTitle, hand.OGDescription, hand.OGImage = " Mine ", "Mine too", "https://cdn.example.test/e.png"
	if c := buildPanel(hand).Card; c.Title != "Mine" || c.Description != "Mine too" || c.Image != "https://cdn.example.test/e.png" {
		t.Errorf("hand-written card = %+v", c)
	}

	site := base
	site.SchemaSlug = "page"
	if c := buildPanel(site).Card; c.Title != "Guide" || c.Image != "https://example.test/media/site.png" {
		t.Errorf("site default card = %+v", c)
	}

	term := base
	term.Social = false
	if buildPanel(term).Card.Show {
		t.Error("a form without social fields gets no card")
	}

	a := newPartialAdmin(t)
	out, err := a.RenderPartial("hm/seopanel-card", map[string]any{"Panel": buildPanel(base)})
	if err != nil || !strings.Contains(string(out), "Guide | Docs") || !strings.Contains(string(out), `id="seo-card"`) {
		t.Errorf("card partial: %v %s", err, out)
	}
}
