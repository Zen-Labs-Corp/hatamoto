package admin

import (
	"net/http/httptest"
	"net/url"
	"slices"
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

// The tab and screen-reader order is the order the narrow layout draws: Content, Publishing,
// Terms, SEO. The wide layout moves SEO under Content with grid areas, not with the DOM.
func TestEntityEditDOMOrderMatchesNarrowLayout(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.do("GET", "/admin/entities/"+fid(11).String(), "owner-token", nil, nil).Body.String()
	last := -1
	for _, mark := range []string{`id="content"`, `id="publishing"`, `id="seo"`} {
		i := strings.Index(body, mark)
		if i <= last {
			t.Fatalf("%s is out of order (at %d, previous at %d): want Content, Publishing, Terms, SEO", mark, i, last)
		}
		last = i
	}
	if i, j := strings.Index(body, `id="terms"`), strings.Index(body, `id="seo"`); i >= 0 && i > j {
		t.Error("Terms must come before SEO")
	}
	has(t, body, `<aside class="hm-entityform__side" aria-label="Publishing and terms">`)
}

func TestSEOFormSchemaKeysAreSeparate(t *testing.T) {
	t.Parallel()

	for _, k := range seoSchemaKeys {
		if slices.Contains(seoKeys, k) {
			t.Errorf("%s is in seoKeys: the builder drawer would wipe it", k)
		}
	}
	m := map[string]any{"schema_type": "Course", "custom_jsonld": "{}", "meta_title": "T"}
	seoForm{Title: "New"}.merge(m)
	if m["schema_type"] != "Course" || m["custom_jsonld"] != "{}" {
		t.Errorf("merge touched the schema keys: %v", m)
	}
	seoForm{SchemaType: " Service ", CustomJSONLD: ""}.mergeSchema(m)
	if m["schema_type"] != "Service" {
		t.Errorf("schema_type = %v", m["schema_type"])
	}
	if _, ok := m["custom_jsonld"]; ok {
		t.Error("empty custom_jsonld kept")
	}
	if f := seoFrom(map[string]any{"schema_type": "Course", "custom_jsonld": "{}"}); f.SchemaType != "Course" || f.CustomJSONLD != "{}" {
		t.Errorf("seoFrom = %+v", f)
	}
}

func TestEntityEditSavesSchemaTabFields(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	ld := `{"@context":"https://schema.org","@type":"Thing","name":"x"}`
	base := url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"S"}}
	post := func(extra url.Values) *httptest.ResponseRecorder {
		form := url.Values{}
		for k, v := range base {
			form[k] = v
		}
		for k, v := range extra {
			form[k] = v
		}
		return c.do("POST", "/admin/entities/"+id, "editor-token", form, nil)
	}

	if rec := post(url.Values{"seo_schema_type": {"Course"}, "seo_custom_jsonld": {ld}}); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	got := attrsMap(c.ents.ents[fid(11)].Attributes)
	if got["schema_type"] != "Course" || got["custom_jsonld"] != ld {
		t.Fatalf("attributes = %v", got)
	}
	// A form without the fields keeps them; an empty field clears them.
	if rec := post(nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := attrsMap(c.ents.ents[fid(11)].Attributes); got["schema_type"] != "Course" || got["custom_jsonld"] != ld {
		t.Errorf("unposted fields were dropped: %v", got)
	}
	if rec := post(url.Values{"seo_schema_type": {""}, "seo_custom_jsonld": {""}}); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := attrsMap(c.ents.ents[fid(11)].Attributes); got["schema_type"] != nil || got["custom_jsonld"] != nil {
		t.Errorf("empty fields kept: %v", got)
	}
}

func TestEntityEditRejectsBadSchemaTabFields(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	before := string(c.ents.ents[fid(11)].Attributes)
	for name, extra := range map[string]url.Values{
		"seo-custom-jsonld": {"seo_custom_jsonld": {`{"@type":"Thing"}`}},
		"seo-schema-type":   {"seo_schema_type": {"FAQPage"}},
		"not-json":          {"seo_custom_jsonld": {`{`}},
	} {
		form := url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"S"}}
		for k, v := range extra {
			form[k] = v
		}
		rec := c.do("POST", "/admin/entities/"+fid(11).String(), "owner-token", form, nil)
		if rec.Code != 422 {
			t.Errorf("%s: save = %d, want 422", name, rec.Code)
			continue
		}
		field := "seo_schema_type"
		if strings.Contains(name, "json") {
			field = "seo_custom_jsonld"
		}
		if !strings.Contains(rec.Body.String(), `href="#`+field+`"`) {
			t.Errorf("%s: no summary link to %s", name, field)
		}
	}
	if got := string(c.ents.ents[fid(11)].Attributes); got != before {
		t.Errorf("a rejected save changed the entity: %s", got)
	}
}
