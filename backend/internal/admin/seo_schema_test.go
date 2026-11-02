package admin

import (
	"net/url"
	"strings"
	"testing"
)

const annotatedSchema = `{"type":"object","x-schema-org-type":"Recipe","properties":{}}`

func newSchemaRig(t *testing.T) *seoRig {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	s.ents.addSchema(5, "Guide", "guide", annotatedSchema, `{}`, false)
	s.post = s.ents.addSchema(6, "Post", "post", `{"type":"object","properties":{}}`, `{}`, true)
	return s
}

func TestSEOSchemaPageLists(t *testing.T) {
	s := newSchemaRig(t)
	body := s.get(t, "/admin/settings/seo/schema")
	for _, want := range []string{
		`name="schema[service][type]"`, `name="schema[recipe][type]"`,
		"Default (Thing)", `name="schema[service][faq]"`, "Set by the type&#39;s schema.", "Person markup is built in.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, name := range []string{"schema[person][type]", "schema[guide][type]"} {
		if strings.Contains(body, `name="`+name+`"`) {
			t.Errorf("type-read-only row posts %s", name)
		}
	}
	for _, name := range []string{"schema[page][type]", "schema[post][type]", "schema[post][subtype]", "schema[person][faq]", "schema[guide][faq]"} {
		if !strings.Contains(body, `name="`+name+`"`) {
			t.Errorf("page lacks %s", name)
		}
	}
	// Only an Article-family type offers the subtype.
	if strings.Contains(body, `name="schema[service][subtype]"`) {
		t.Error("subtype offered for a Thing type")
	}
}

func TestSEOSchemaSavePatchesOnlyChangedRows(t *testing.T) {
	s := newSchemaRig(t)
	s.ents.schemas[0].Settings = []byte(`{"url_pattern":"/services/{path}","schema_org_type":"Article"}`)
	form := url.Values{
		"schema[service][type]": {"Article"}, "schema[service][subtype]": {"BlogPosting"}, "schema[service][faq]": {"true"},
		"schema[recipe][type]": {"Course"}, "schema[recipe][faq]": {},
		"schema[person][type]": {"Service"}, "schema[guide][type]": {"Service"},
	}
	rec := s.do("POST", "/admin/settings/seo/schema", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/schema" {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	svc := s.svc
	if len(svc.patches) != 2 {
		t.Fatalf("patches = %+v", svc.patches)
	}
	svcP := svc.patches[s.service.ID]
	if svcP.SchemaOrgType != nil || svcP.FAQFromContent != nil || svcP.ArticleType == nil || *svcP.ArticleType != "BlogPosting" {
		t.Errorf("service patch = %+v", svcP)
	}
	rp := svc.patches[s.recipe.ID]
	if rp.SchemaOrgType == nil || *rp.SchemaOrgType != "Course" || rp.FAQFromContent == nil || *rp.FAQFromContent {
		t.Errorf("recipe patch = %+v", rp)
	}
	if s.flushed != 1 || len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Errorf("flushed %d, audit %+v", s.flushed, s.events)
	}
}

func TestSEOSchemaSaveUnchangedWritesNothing(t *testing.T) {
	s := newSchemaRig(t)
	form := url.Values{"schema[service][type]": {""}, "schema[service][faq]": {"true"}, "schema[recipe][type]": {""}, "schema[recipe][faq]": {"true"}}
	if rec := s.do("POST", "/admin/settings/seo/schema", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if len(s.svc.patches) != 0 || s.flushed != 0 {
		t.Errorf("patches %+v, flushed %d", s.svc.patches, s.flushed)
	}
}

func TestSEOSchemaSaveRejectsUnknownTypes(t *testing.T) {
	s := newSchemaRig(t)
	form := url.Values{"schema[service][type]": {"FAQPage"}, "schema[recipe][type]": {"Article"}, "schema[recipe][subtype]": {"Poem"}}
	rec := s.do("POST", "/admin/settings/seo/schema", "owner-token", form, nil)
	if rec.Code != 422 {
		t.Fatalf("save = %d", rec.Code)
	}
	for _, want := range []string{`href="#schema-service-type"`, `href="#schema-recipe-subtype"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("no error link %q", want)
		}
	}
	if len(s.svc.patches) != 0 {
		t.Errorf("patched: %+v", s.svc.patches)
	}
}

func TestSEOSchemaOwnerOnly(t *testing.T) {
	s := newSchemaRig(t)
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/schema", "editor-token", url.Values{"schema[service][type]": {"Article"}}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
}

// The Save footer sits after the closed cards: a stray closer pops the form
// and the page column.
func TestSEOSchemaPageMarkupBalanced(t *testing.T) {
	s := newSchemaRig(t)
	body := s.get(t, "/admin/settings/seo/schema")
	for _, tag := range []string{"div", "section", "form"} {
		if o, c := strings.Count(body, "<"+tag), strings.Count(body, "</"+tag+">"); o != c {
			t.Errorf("<%s> opened %d times, closed %d", tag, o, c)
		}
	}
}

// Pages and Posts are built in, yet their type, subtype and FAQ switch save.
func TestSEOSchemaSavesBuiltinRows(t *testing.T) {
	s := newSchemaRig(t)
	form := url.Values{"schema[page][type]": {"Service"}, "schema[page][faq]": {"false"}, "schema[post][type]": {""}, "schema[post][subtype]": {"BlogPosting"}, "schema[post][faq]": {"true"}}
	if rec := s.do("POST", "/admin/settings/seo/schema", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	pp := s.svc.patches[s.page.ID]
	if pp.SchemaOrgType == nil || *pp.SchemaOrgType != "Service" || pp.FAQFromContent == nil || *pp.FAQFromContent {
		t.Errorf("page patch = %+v", pp)
	}
	if op := s.svc.patches[s.post.ID]; op.ArticleType == nil || *op.ArticleType != "BlogPosting" || op.SchemaOrgType != nil {
		t.Errorf("post patch = %+v", op)
	}
}

// The type select is read-only for person and annotated types: a posted type is
// ignored, the FAQ switch still saves.
func TestSEOSchemaTypeReadOnlyRowsKeepTypeSaveFAQ(t *testing.T) {
	s := newSchemaRig(t)
	form := url.Values{
		"schema[person][type]": {"Service"}, "schema[person][faq]": {"false"},
		"schema[guide][type]": {"Service"}, "schema[guide][faq]": {"false"},
	}
	if rec := s.do("POST", "/admin/settings/seo/schema", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if len(s.svc.patches) != 2 {
		t.Fatalf("patches = %+v", s.svc.patches)
	}
	for id, p := range s.svc.patches {
		if p.SchemaOrgType != nil || p.FAQFromContent == nil || *p.FAQFromContent {
			t.Errorf("%s patch = %+v, want FAQ off and no type", id, p)
		}
	}
}

// An annotated type whose @type is an Article keeps the subtype select.
func TestSEOSchemaAnnotatedArticleOffersSubtype(t *testing.T) {
	s := newSchemaRig(t)
	s.ents.addSchema(7, "Essay", "essay", `{"type":"object","x-schema-org-type":"Article","properties":{}}`, `{}`, false)
	body := s.get(t, "/admin/settings/seo/schema")
	if !strings.Contains(body, `name="schema[essay][subtype]"`) || strings.Contains(body, `name="schema[essay][type]"`) {
		t.Error("essay: want the subtype select and no type select")
	}
	if strings.Contains(body, `name="schema[guide][subtype]"`) {
		t.Error("a Recipe annotation offers the subtype")
	}
}
