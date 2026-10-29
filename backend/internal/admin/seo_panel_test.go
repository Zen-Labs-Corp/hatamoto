package admin

import (
	"testing"

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
