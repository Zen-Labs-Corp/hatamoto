package admin

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

func TestEntityEditUsesTheSharedSEOPanel(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	body := c.do("GET", "/admin/entities/"+id, "owner-token", nil, nil).Body.String()
	has(t, body, `data-hm-seo-tabs`, `id="seo-score"`, `id="seo-tab-advanced"`, `name="seo_title"`, `name="seo_description"`, `name="seo_og_image"`,
		`name="seo_canonical"`, `name="seo_noindex"`, `id="seo-schema"`, `hx-post="/admin/entities/`+id+`/analyze"`, `hx-include="#hm-entityform"`, `id="hm-entityform"`)
	lacks(t, body, `name="seo_keyword"`, `name="seo_llms"`, "Focus keyword", "llms.txt", "Answerability")

	fresh := c.do("GET", "/admin/entities/new?type=service", "owner-token", nil, nil).Body.String()
	has(t, fresh, `data-hm-seo-tabs`, `id="seo-score"`)
	lacks(t, fresh, `hx-post="/admin/entities/`)
}

func TestEntityAnalyzeAnswersTheFragmentAndWritesNothing(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	before := string(c.ents.ents[fid(11)].Attributes)
	form := url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "seo_title": {"A hand written title for the alpha service"}, "seo_description": {"Short"}}
	rec := c.do("POST", "/admin/entities/"+id+"/analyze", "owner-token", form, map[string]string{"HX-Request": "true", "HX-Target": "be-panel"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, `class="hm-shell"`) {
		t.Error("the panel is a fragment")
	}
	has(t, body, "checks pass", "Search result preview", "A hand written title for the alpha service", `id="seo-score"`, `id="seo-title-count"`, `id="seo-schema"`, "/services/alpha")
	lacks(t, body, "Focus keyword", "llms.txt", "Answerability", "Internal links", "Readability")
	if got := string(c.ents.ents[fid(11)].Attributes); got != before || c.ents.ents[fid(11)].Version != 1 {
		t.Errorf("analysing changed the entity: %s (version %d)", got, c.ents.ents[fid(11)].Version)
	}
	if rec := c.do("POST", "/admin/entities/"+id+"/analyze", "", form, nil); rec.Code == 200 {
		t.Error("analyze without a session")
	}
	if rec := c.do("POST", "/admin/entities/"+fid(99).String()+"/analyze", "owner-token", form, nil); rec.Code != 404 {
		t.Errorf("unknown entity: %d", rec.Code)
	}
}

func TestEntityPanelReadsTheBodyWhenTheTypeHasOne(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.ents.addEntity(61, c.page, "About", "about", entity.StatusDraft, `{"body":"<h2>Who we are</h2><p>`+strings.Repeat("word ", 40)+`</p>"}`, time.Now())
	form := url.Values{"name": {"About"}, "slug": {"about"}, "attr.body": {"<h2>Who we are</h2><p>" + strings.Repeat("word ", 40) + "</p>"}}
	rec := c.do("POST", "/admin/entities/"+fid(61).String()+"/analyze", "owner-token", form, map[string]string{"HX-Request": "true"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	has(t, rec.Body.String(), "Headings", "Length", "Readability")
	lacks(t, rec.Body.String(), "Focus keyword", "llms.txt")
}
