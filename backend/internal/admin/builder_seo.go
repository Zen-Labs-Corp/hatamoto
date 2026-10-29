package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// seoGate is builderGate for pages only: a reuse block or theme part has no
// address, so no SEO.
func (a *Admin) seoGate(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.EntitySchema, bool) {
	e, sc, ok := a.builderGate(w, r)
	if !ok {
		return nil, nil, false
	}
	if a.entityRole(r.Context(), e) != rolePage {
		a.Error(w, r, http.StatusNotFound, "No SEO", "Only pages have SEO settings.")
		return nil, nil, false
	}
	return e, sc, true
}

// handleBuilderSEO is GET /admin/entities/{id}/builder/seo: the SEO panel the
// builder's drawer shows, the stored values with the live checks wired to the
// analyze route. It writes nothing.
func (a *Admin) handleBuilderSEO(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.seoGate(w, r)
	if !ok {
		return
	}
	f := seoFrom(attrsMap(e.Attributes))
	in := a.builderSEOInput(r, e, sc, f, "")
	v := a.NewView(w, r, "SEO")
	v.Data = struct {
		Panel      seoPanel
		SEO        seoForm
		Errors     map[string]string
		Noun       string
		AnalyzeURL string
	}{a.builderPanel(e, sc, in), f, nil, "page", a.adminPath + "/entities/" + e.ID.String() + "/builder/analyze"}
	a.Render(w, r, http.StatusOK, "entities/builderseo", v)
}

// analyzeFormSlack is the room the SEO fields take next to the doc.
const analyzeFormSlack = 64 << 10

// handleBuilderAnalyze is POST /admin/entities/{id}/builder/analyze: the SEO
// panel for the draft in the request. It renders the posted doc through the
// preview, reads the page's <main> and runs the body checks on it. Nothing is
// stored.
func (a *Admin) handleBuilderAnalyze(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.seoGate(w, r)
	if !ok {
		return
	}
	if a.deps.Content.Preview == nil {
		a.Error(w, r, http.StatusNotFound, "No preview", "The page preview is not available.")
		return
	}
	// The doc travels as a form field, where each of its quotes, braces and
	// colons takes three bytes: the cap leaves room for a doc that saves.
	r.Body = http.MaxBytesReader(w, r.Body, 3*builder.MaxBytes+analyzeFormSlack)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.Error(w, r, http.StatusRequestEntityTooLarge, "Too large", "This page is too large to analyse.")
			return
		}
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The request could not be read.")
		return
	}
	doc := []byte(r.PostForm.Get("doc"))
	if _, err := builder.Parse(doc); err != nil {
		a.Error(w, r, http.StatusUnprocessableEntity, "Invalid page", err.Error())
		return
	}
	req := a.builderBase(r, e, sc, false)
	req["attributes"].(map[string]json.RawMessage)["page_doc"] = doc
	body, err := json.Marshal(req)
	if err != nil {
		a.serverError(w, r, "The page could not be analysed", err)
		return
	}
	rec := httptest.NewRecorder()
	a.servePreview(rec, r, body)
	if rec.Code != http.StatusOK {
		a.Error(w, r, http.StatusBadGateway, "No preview", "The page could not be rendered for analysis.")
		return
	}
	in := a.builderSEOInput(r, e, sc, seoFromForm(r), mainHTML(rec.Body.Bytes()))
	v := a.NewView(w, r, "Search checks")
	v.Data = struct{ Panel seoPanel }{a.builderPanel(e, sc, in)}
	a.Render(w, r, http.StatusOK, "entities/seopanel", v)
}

// builderPanel is the panel for in with the structured data the stored page
// will carry, built as the entity form builds it.
func (a *Admin) builderPanel(e *entity.Entity, sc *entity.EntitySchema, in seoInput) seoPanel {
	panel := buildPanel(in)
	panel.JSONLD, panel.JSONLDType = a.jsonLDPreview(e, sc, &posted{f: &entityForm{Name: e.Name, Slug: e.Slug}, attrs: attrsMap(e.Attributes)}, in)
	return panel
}

// builderSEOInput is the panel input the two routes share: the page as named
// and addressed in the store, the SEO fields as given, content as the body. The
// builder has no focus-keyword or llms.txt field, so those checks are left out.
func (a *Admin) builderSEOInput(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, f seoForm, content string) seoInput {
	in := seoInput{
		Title: e.Name, MetaTitle: f.Title, MetaDescription: f.Description, Slug: e.Slug, Canonical: f.Canonical,
		Excerpt: str(attrsMap(e.Attributes), "excerpt"), Content: content, Published: e.Status == entity.StatusPublished,
		Mode: ModeBody, NoKeyword: true, NoLLMs: true,
	}
	if a.deps.BaseURL != nil {
		in.BaseURL = a.deps.BaseURL(r)
	}
	if a.deps.Content.PublicPath != nil {
		in.Path = a.deps.Content.PublicPath(r.Context(), e)
	}
	if in.Path == "" {
		in.Path = "/" + e.Slug
	}
	a.withSEOSettings(r, &in, sc.Slug)
	a.markHome(r.Context(), &in, e.ID)
	return in
}

// mainHTML is the inner HTML of the first <main> of a rendered page, so the
// checks read the content and not the theme's header, navigation and footer.
// A page with no <main> falls back to its <body>.
func mainHTML(page []byte) string {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return ""
	}
	var find func(n *html.Node, tag string) *html.Node
	find = func(n *html.Node, tag string) *html.Node {
		if n.Type == html.ElementNode && n.Data == tag {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if got := find(c, tag); got != nil {
				return got
			}
		}
		return nil
	}
	root := find(doc, "main")
	if root == nil {
		root = find(doc, "body")
	}
	if root == nil {
		return ""
	}
	var sb strings.Builder
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&sb, c)
	}
	return sb.String()
}
