package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// guideSchema is shaped like a custom content type: title and content are
// required, and title is an attribute of its own next to the entity name.
const guideSchema = `{"type":"object","required":["title","content"],"properties":{"title":{"type":"string"},"content":{"type":"string"},"excerpt":{"type":"string","maxLength":300},"cluster":{"type":"string"},"cluster_order":{"type":"integer"},"is_pillar":{"type":"boolean"},"page_sections":{"type":"array"}}}`

// guideRig adds two custom types: guide opts in to the post editor through
// settings.editor, faq does not.
type guideRig struct {
	*blogRig
	guide *entity.Entity
}

func newGuideRig(t *testing.T) *guideRig {
	r := newDraftRig(t)
	sc := r.ents.addSchema(23, "Guide", "guide", guideSchema, `{"url_pattern":"/guides/{slug}","editor":"post"}`, false)
	e := r.ents.addEntity(81, sc, "Ship Go", "ship-go", entity.StatusDraft,
		`{"title":"Ship Go","content":"<p>Body</p>","cluster":"go","cluster_order":2,"is_pillar":true,"page_sections":[{"type":"hero"}]}`, t0)
	return &guideRig{blogRig: r, guide: e}
}

func (g *guideRig) path(suffix string) string {
	return "/admin/entities/" + g.guide.ID.String() + suffix
}

func TestPostEditorForSetting(t *testing.T) {
	t.Parallel()

	mk := func(slug, settings string) *entity.EntitySchema {
		return &entity.EntitySchema{Slug: slug, Settings: []byte(settings)}
	}
	for slug, want := range map[string]bool{"post": true, "article": true, "blog_post": true, "blog": true, "guide": false} {
		if got := postEditorFor(mk(slug, ``)); got != want {
			t.Errorf("%s without settings: %v", slug, got)
		}
	}
	if !postEditorFor(mk("guide", `{"editor":"post"}`)) || postEditorFor(mk("guide", `{"editor":"form"}`)) || postEditorFor(nil) {
		t.Error("settings.editor is not honoured exactly")
	}
}

func TestOptedInTypeOpensThePostEditor(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	body := g.get(t, "owner-token", g.path("/edit"), "")
	has(t, body, `data-hm-blog-editor`, `name="content"`)
	lacks(t, body, `data-hm-page-editor`)
}

func TestOptedInTypeRoutesAcceptIt(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	f := url.Values{"version": {"1"}, "name": {"Ship Go now"}, "content": {"<p>Typing</p>"}}
	if rec := g.post(g.path("/edit/autosave"), f); rec.Code != http.StatusOK {
		t.Errorf("autosave: %d %s", rec.Code, rec.Body.String())
	}
	// preview and analyze answer 200 here; on a type without the setting they are 404.
	for _, suffix := range []string{"/edit/preview", "/edit/analyze"} {
		rec := g.do("POST", g.path(suffix), "owner-token", g.form(t, f), map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", suffix, rec.Code, rec.Body.String())
		}
	}
	// The link routes have no type gate, so they are not part of this test.
}

func (g *guideRig) form(t *testing.T, over url.Values) url.Values {
	t.Helper()
	f := parseForm(t, g.get(t, "owner-token", g.path("/edit"), ""))
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestOptedInTypeNewStartsADraft(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	rec := g.do("GET", "/admin/entities/new?type=guide", "owner-token", nil, nil)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `action="/admin/entities/drafts"`) || !strings.Contains(body, "Start a new guide") {
		t.Fatalf("confirm page: %d %s", rec.Code, body)
	}
	before := g.drafts()
	rec = g.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"guide"}}, nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasSuffix(loc, "/edit") || g.drafts() != before+1 {
		t.Fatalf("draft: %d %q drafts=%d", rec.Code, loc, g.drafts())
	}
}

func TestOptedInTypeListLinksToTheEditor(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	body := g.get(t, "owner-token", "/admin/entities?type=guide", "")
	has(t, body, `href="`+g.path("/edit")+`"`, `action="/admin/entities/drafts"`)
	all := g.get(t, "owner-token", "/admin/entities", "")
	has(t, all, `name="type" value="guide"`)
	lacks(t, all, "new?type=guide")
	has(t, all, "new?type=service")
}

func TestOptedInTypeSaveKeepsItsAttributes(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	f := g.form(t, url.Values{"content": {"<p>New body</p>"}, "_op": {"save"}})
	if rec := g.post(g.path("/edit"), f); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := attrsMap(g.ents.ents[g.guide.ID].Attributes)
	if got["content"] != "<p>New body</p>" || got["cluster"] != "go" || got["is_pillar"] != true || got["page_sections"] == nil {
		t.Errorf("stored: %v", got)
	}
}

func TestOptedInTypeDetailsPageStillEditsTheRest(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	body := g.get(t, "owner-token", g.path(""), "")
	has(t, body, `cluster`, `is_pillar`, g.path("/edit"))
}

func TestOptedInTypeNewDraftSatisfiesRequiredProperties(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	rec := g.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"guide"}}, nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasSuffix(loc, "/edit") {
		t.Fatalf("draft: %d %q", rec.Code, loc)
	}
	var found bool
	for _, e := range g.ents.ents {
		if e.Name == "Untitled guide" {
			found = true
			got := attrsMap(e.Attributes)
			if got["title"] != "Untitled guide" || got["content"] != "" {
				t.Errorf("seeded attributes: %v", got)
			}
		}
	}
	if !found {
		t.Fatal("no draft was created")
	}
}

func TestSeedRequiredZeroValues(t *testing.T) {
	t.Parallel()

	sc := &entity.EntitySchema{Schema: []byte(`{"required":["title","n","b","l","o","s"],"properties":{"title":{"type":"string"},"n":{"type":"integer"},"b":{"type":"boolean"},"l":{"type":"array"},"o":{"type":"object"},"s":{"type":"string"}}}`)}
	if got := string(seedRequired(sc, "T")); got != `{"b":false,"l":[],"n":0,"o":{},"s":"","title":"T"}` {
		t.Errorf("seed = %s", got)
	}
}

func TestOptedInTypeMirrorsTheNameIntoTitle(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	f := url.Values{"version": {"1"}, "name": {"Autosaved name"}}
	if rec := g.post(g.path("/edit/autosave"), f); rec.Code != http.StatusOK {
		t.Fatalf("autosave: %d %s", rec.Code, rec.Body.String())
	}
	if got := attrsMap(g.ents.ents[g.guide.ID].Attributes)["title"]; got != "Autosaved name" {
		t.Errorf("title after autosave = %v", got)
	}
	f = g.form(t, url.Values{"name": {"Saved name"}, "_op": {"save"}})
	if rec := g.post(g.path("/edit"), f); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if got := attrsMap(g.ents.ents[g.guide.ID].Attributes)["title"]; got != "Saved name" {
		t.Errorf("title after save = %v", got)
	}
}

func TestOptedInTypeShowsSchemaValidationErrors(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	// cluster_order is typed as an integer; a stored string makes every save invalid.
	g.ents.ents[g.guide.ID].Attributes = []byte(`{"title":"Ship Go","content":"<p>x</p>","cluster_order":"first"}`)
	f := g.form(t, url.Values{"_op": {"save"}})
	rec := g.post(g.path("/edit"), f)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Cluster order") {
		t.Errorf("save: %d, error not shown: %s", rec.Code, rec.Body.String())
	}
	rec = g.post(g.path("/edit/autosave"), url.Values{"version": {"1"}, "name": {"X"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"error":"`) {
		t.Errorf("autosave: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOptedInTypeEditorUsesTheTypeName(t *testing.T) {
	t.Parallel()

	g := newGuideRig(t)
	body := g.get(t, "owner-token", g.path("/edit"), "")
	has(t, body, `placeholder="Untitled guide"`)
	lacks(t, body, "Untitled post")
}
