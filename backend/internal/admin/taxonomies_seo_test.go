package admin

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
)

func (c *contentRig) termSEOPost(n int, token string, form url.Values, json bool) *httptest.ResponseRecorder {
	hdr := map[string]string{}
	if json {
		hdr["Accept"] = "application/json"
	}
	return c.do("POST", "/admin/settings/taxonomies/terms/"+fid(n).String()+"/seo", token, form, hdr)
}

func TestTermsSEOViewGolden(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.terms.terms[1].SEO = &taxonomy.TermSEO{MetaTitle: "Press room", MetaDescription: "All the press", NoIndex: true, CanonicalURL: "/c"}
	c.a.deps.SEO = staticSEO{set: seo.Settings{Separator: "|", Taxonomy: map[string]string{"category": "%term% archive"}}}
	body := c.get(t, "owner-token", "/admin/settings/taxonomies?kind=category&view=seo", "taxonomies-list-seo")
	p := fid(32).String()
	has(t, body,
		">Term<", ">SEO title<", ">Noindex<",
		`id="seo-`+p+`-title" name="title" form="seo-`+p+`" value="Press room"`,
		`placeholder="News archive"`,
		`name="back" value="/admin/settings/taxonomies?kind=category&amp;view=seo"`,
		`action="/admin/settings/taxonomies/terms/`+p+`/seo"`,
		`href="/admin/settings/taxonomies?kind=tag&amp;view=seo"`, `aria-label="Columns"`,
	)
	lacks(t, body, `name="keyword"`, `data-hm-select-all`)
}

func TestTermSEOInlineKeepsCanonicalParentAndSlug(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.terms.terms[1].SEO = &taxonomy.TermSEO{MetaTitle: "Old", CanonicalURL: "/c", OGImage: "/og.png"}
	rows := c.record()
	form := url.Values{"title": {"New title"}, "orig_title": {"Old"}, "description": {"D"}, "orig_description": {""}, "noindex": {"true"}, "orig_noindex": {""}}
	rec := c.termSEOPost(32, "owner-token", form, true)
	if rec.Code != 200 {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}
	if r := decodeSEOReply(t, rec); r.Status != "saved" || r.Values.Title != "New title" || !r.Values.NoIndex {
		t.Errorf("reply = %+v", r)
	}
	tm := c.terms.terms[1]
	if tm.SEO.MetaTitle != "New title" || tm.SEO.MetaDescription != "D" || !tm.SEO.NoIndex || tm.SEO.CanonicalURL != "/c" || tm.SEO.OGImage != "/og.png" {
		t.Errorf("seo = %+v", tm.SEO)
	}
	if tm.ParentID == nil || *tm.ParentID != fid(31) || tm.Slug != "press" || tm.Name != "Press" {
		t.Errorf("term = %+v", tm)
	}
	got := rows()
	if len(got) != 1 || got[0].Action != "term.seo_update" || got[0].Detail["name"] != "Press" || strings.Join(got[0].Detail["fields"].([]string), ",") != "title,description,noindex" {
		t.Fatalf("audit = %+v", got)
	}
}

func TestTermSEOInlineConflictNoJSAndRoles(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.terms.terms[1].SEO = &taxonomy.TermSEO{MetaTitle: "Changed elsewhere"}
	rec := c.termSEOPost(32, "owner-token", url.Values{"title": {"Mine"}, "orig_title": {"Old"}, "noindex": {"true"}, "orig_noindex": {""}}, true)
	r := decodeSEOReply(t, rec)
	if rec.Code != 409 || r.Status != "conflict" || r.Conflicts["title"] != "Changed elsewhere" {
		t.Fatalf("conflict = %d %+v", rec.Code, r)
	}
	if s := c.terms.terms[1].SEO; s.MetaTitle != "Changed elsewhere" || !s.NoIndex {
		t.Errorf("seo = %+v (title kept, noindex saved)", s)
	}
	rec = c.termSEOPost(32, "owner-token", url.Values{"description": {"x"}, "orig_description": {""}, "back": {"/admin/settings/taxonomies?kind=category&view=seo"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/taxonomies?kind=category&view=seo" {
		t.Errorf("no-JS = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = c.termSEOPost(32, "editor-token", url.Values{"title": {"x"}, "orig_title": {""}}, true); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	// A bad value is refused with the field's message.
	rec = c.termSEOPost(32, "owner-token", url.Values{"title": {strings.Repeat("x", 300)}, "orig_title": {"Changed elsewhere"}}, true)
	if rec.Code != 422 || decodeSEOReply(t, rec).Status != "error" {
		t.Errorf("long title = %d %s", rec.Code, rec.Body)
	}
}
