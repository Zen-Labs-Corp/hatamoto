package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

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
// The panel also serves pages and custom entity types, so no check message
// may say "post".
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

func TestSEOPanelNodeSwitchOptions(t *testing.T) {
	t.Parallel()

	sc := &entity.EntitySchema{Slug: "page", Schema: []byte(`{"type":"object"}`), Settings: []byte(`{"emit_webpage":"off"}`)}
	var p seoPanel
	p.withSchemaFields(sc, seoInput{JSONLDBreadcrumb: "on"})
	if got := p.WebPageOptions[0]; got.Label != "Use schema default (Off)" || !got.Selected {
		t.Errorf("webpage first option = %+v", got)
	}
	if got := p.BreadcrumbOptions; got[0].Label != "Use schema default (Auto)" || got[0].Selected || !got[1].Selected || got[2].Selected {
		t.Errorf("breadcrumb options = %+v", got)
	}
}

func TestSEOPanelSchemaFields(t *testing.T) {
	t.Parallel()

	sc := &entity.EntitySchema{Slug: "page", Schema: []byte(`{"type":"object"}`), Settings: []byte(`{}`)}
	var p seoPanel
	p.withSchemaFields(sc, seoInput{SchemaType: "Course", CustomJSONLD: "{}"})
	if !p.ShowSchemaFields || p.CustomJSONLD != "{}" {
		t.Errorf("panel = %+v", p)
	}
	if got := p.SchemaTypes[0]; got.Value != "" || got.Label != "Use type default (WebPage)" || got.Selected {
		t.Errorf("first option = %+v", got)
	}
	selected := 0
	for _, o := range p.SchemaTypes {
		if o.Selected {
			selected++
			if o.Value != "Course" {
				t.Errorf("selected %q", o.Value)
			}
		}
	}
	if selected != 1 || len(p.SchemaTypes) != 1+len(entity.AllowedSchemaOrgTypes) {
		t.Errorf("options = %+v", p.SchemaTypes)
	}

	// A stored type setting is the default the label names; an annotated schema has its own.
	sc = &entity.EntitySchema{Slug: "doc", Schema: []byte(`{"type":"object"}`), Settings: []byte(`{"schema_org_type":"Article"}`)}
	p = seoPanel{}
	p.withSchemaFields(sc, seoInput{})
	if p.SchemaTypes[0].Label != "Use type default (Article)" || !p.SchemaTypes[0].Selected {
		t.Errorf("first option = %+v", p.SchemaTypes[0])
	}
	sc = &entity.EntitySchema{Slug: "guide", Schema: []byte(`{"type":"object","x-schema-org-type":"Recipe"}`), Settings: []byte(`{}`)}
	p = seoPanel{}
	p.withSchemaFields(sc, seoInput{})
	if p.SchemaTypes[0].Label != "Use type default (the type's schema)" {
		t.Errorf("first option = %+v", p.SchemaTypes[0])
	}
}

func TestSEOPanelCardPartial(t *testing.T) {
	t.Parallel()

	set := seo.Defaults()
	set.Social.Types = map[string]seo.SocialType{"doc": {Title: "%title% %sep% Docs"}}
	base := seoInput{Title: "Guide", MetaDescription: "Meta words.", BaseURL: "https://example.test", Slug: "guide", Path: "/guide", Social: true, SEO: &set, SchemaSlug: "doc", SiteName: "Example"}
	a := newPartialAdmin(t)
	out, err := a.RenderPartial("hm/seopanel-card", map[string]any{"Panel": buildPanel(base)})
	if err != nil || !strings.Contains(string(out), "Guide | Docs") || !strings.Contains(string(out), `id="seo-card"`) {
		t.Errorf("card partial: %v %s", err, out)
	}
}
