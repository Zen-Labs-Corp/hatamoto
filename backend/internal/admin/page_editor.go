package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
)

// The page editor is the builder (builder_editor.go): every entity that carries
// a page body opens it on its page_doc, or on the in-memory conversion of its
// legacy page_sections (loadPageEntity). This file routes the editor screens
// and keeps the pieces they share: the staged preview store and its frame.

// postEditors are the entity types whose body is written in the blog editor
// (blog_editor.go), not the page editor, even when they carry sections.
var postEditors = map[string]bool{"post": true, "article": true, "blog_post": true, "blog": true}

// postEditorFor reports whether sc is edited in the post editor.
func postEditorFor(sc *entity.EntitySchema) bool { return entity.IsPostEditorSchema(sc) }

// pageEditorFor reports whether the Go page editor edits e: the page type and
// every other entity that carries sections (a non-empty page_sections, or a
// schema that declares the property). A person never does: sections stored on
// one are ignored by the router, and the generic form edits it.
func pageEditorFor(sc *entity.EntitySchema, e *entity.Entity) bool {
	if sc != nil && (postEditorFor(sc) || sc.Slug == personSchemaSlug) {
		return false
	}
	if sc != nil && (sc.Slug == "page" || entity.IsReuseSchema(sc)) {
		return true
	}
	return hasSections(sc, e)
}

// personSchemaSlug is the built-in people content type (internal/person).
const personSchemaSlug = "person"

func hasSections(sc *entity.EntitySchema, e *entity.Entity) bool {
	if e != nil {
		var attrs struct {
			Sections []json.RawMessage `json:"page_sections"`
		}
		if json.Unmarshal(e.Attributes, &attrs) == nil && len(attrs.Sections) > 0 {
			return true
		}
	}
	if sc != nil {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(sc.Schema, &s) == nil {
			_, ok := s.Properties["page_sections"]
			return ok
		}
	}
	return false
}

func (a *Admin) mountPageEditor(authed *httpx.Group, public *httpx.Group) {
	authed.HandleFunc("GET /entities/{id}/edit", a.handleEntityEditor)
	authed.HandleFunc("POST /entities/{id}/edit", a.handlePageEditorPost)
	authed.HandleFunc("POST /entities/{id}/edit/preview", a.handlePreviewPost) // the blog editor's preview
	authed.HandleFunc("POST /entities/{id}/edit/render", a.handlePageRender)
	authed.HandleFunc("POST /entities/{id}/builder/stage", a.handleBuilderStage)
	authed.HandleFunc("POST /entities/{id}/builder/component", a.handleBuilderComponent)
	authed.HandleFunc("POST /entities/{id}/builder/save", a.handleBuilderSave)
	authed.HandleFunc("GET /entities/{id}/builder/seo", a.handleBuilderSEO)
	authed.HandleFunc("GET /entities/{id}/builder/translations", a.handleBuilderTranslations)
	authed.HandleFunc("POST /entities/{id}/builder/analyze", a.handleBuilderAnalyze)
	authed.HandleFunc("POST /entities/{id}/builder/recovery", a.handleBuilderRecoveryPut)
	authed.HandleFunc("POST /entities/{id}/builder/recovery/discard", a.handleBuilderRecoveryDiscard)
	authed.HandleFunc("GET /builder/stylespec", a.handleStyleSpec)
	a.mountBuilderReuse(authed)
	// Editors create presets from the inspector; updating and deleting is the owner's, in Settings.
	authed.Group("", a.RequireRole("owner", "editor")).HandleFunc("POST /builder/presets", a.handlePresetCreate)
	a.mountBlogEditor(authed)
	// The adminguard verifies the one-time frame ticket in the URL (session and
	// entity bound) before this handler runs; see frameticket.go.
	public.HandleFunc("GET /entities/{id}/edit/preview/{token}", a.handlePreviewFrame)
}

func (a *Admin) themeTypes() (map[string]ThemeSection, []ThemeSection) {
	byType := map[string]ThemeSection{}
	var list []ThemeSection
	if a.deps.Content.Theme != nil {
		list = a.deps.Content.Theme()
	}
	for i, t := range list {
		if len(t.Schema) == 0 {
			list[i].Schema = json.RawMessage(`{"type":"object"}`) // a type without a schema has no fields to edit
		}
		byType[t.Type] = list[i]
	}
	return byType, list
}

// untitledPage is the name a new page is created with (entity_draft.go).
const untitledPage = "Untitled page"

// untitledForPublish reports that a page named title still has its generated
// name and address, so publishing it would put them live by accident.
func untitledForPublish(title string, e *entity.Entity) bool {
	return title == untitledPage && generatedSlug(e.Slug)
}

// setPageName puts a changed title on in. The generated address follows the
// first real title until the page is first published; a chosen or live one is
// left alone.
func setPageName(in *entity.UpdateEntityInput, e *entity.Entity, title string) {
	if title == e.Name {
		return
	}
	in.Name = &title
	if slug := slugifyName(title); generatedSlug(e.Slug) && neverPublished(e) && slug != "" {
		in.Slug = &slug
	}
}

func staleMessage(posted, current int) string {
	return fmt.Sprintf("This page was saved by someone else (version %d, you opened version %d). Your changes are still here and nothing was saved. Save again to replace the newer version, or open the versions tab to compare.", current, posted)
}

// ---- preview -----------------------------------------------------------------

// servePreview runs the render preview handler with body as its request.
func (a *Admin) servePreview(w http.ResponseWriter, r *http.Request, body []byte) {
	serveJSON(a.deps.Content.Preview, w, r, body)
}

// serveJSON runs h with body as the JSON body of a POST derived from r.
func serveJSON(h http.Handler, w http.ResponseWriter, r *http.Request, body []byte) {
	pr := r.Clone(r.Context())
	pr.Method = http.MethodPost
	pr.Body = readCloser{bytes.NewReader(body)}
	pr.ContentLength = int64(len(body))
	pr.Header = r.Header.Clone()
	pr.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, pr)
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

// handlePreviewFrame serves a staged preview. The guard has already verified and
// spent the frame ticket in the URL and left the staged body on the context;
// anything else is refused like an unknown admin URL.
func (a *Admin) handlePreviewFrame(w http.ResponseWriter, r *http.Request) {
	body, ok := previewPayload(r.Context())
	if !ok || a.deps.Content.Preview == nil {
		a.refuse(w, r)
		return
	}
	a.servePreview(w, r, body)
}

// loadPageEntity is loadEntity for the builder's screens. A page that has no
// page_doc yet is shown with the in-memory conversion of its page_sections (an
// empty doc when it has none): nothing is written, the first save stores it.
func (a *Admin) loadPageEntity(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.EntitySchema, bool) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return nil, nil, false
	}
	withLegacyDoc(e, sc)
	return e, sc, true
}

// withLegacyDoc sets e's attributes page_doc to the conversion of its
// page_sections when the builder edits e and it has no page_doc. In memory only.
func withLegacyDoc(e *entity.Entity, sc *entity.EntitySchema) {
	if !pageEditorFor(sc, e) {
		return
	}
	if _, state := builderDoc(e); state != docNone {
		return
	}
	doc, ok := entity.LegacyPageDoc(e.Attributes)
	if !ok {
		doc = builder.FromSections(nil) // no sections: an empty page
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return
	}
	attrs := map[string]json.RawMessage{}
	_ = json.Unmarshal(e.Attributes, &attrs)
	if attrs == nil {
		attrs = map[string]json.RawMessage{}
	}
	attrs["page_doc"] = raw
	if out, err := json.Marshal(attrs); err == nil {
		e.Attributes = out
	}
}

func (a *Admin) handleEntityEditor(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadPageEntity(w, r)
	if !ok {
		return
	}
	if blogEditorFor(sc) {
		a.handleBlogEditor(w, r, e, sc)
		return
	}
	if pageEditorFor(sc, e) {
		doc, state := builderDoc(e)
		if state == docOK {
			a.handleBuilderScreen(w, r, e, sc, doc)
			return
		}
		a.renderBrokenDoc(w, r, e, state)
		return
	}
	Redirect(w, r, a.adminPath+"/entities/"+e.ID.String())
}

// renderBrokenDoc is the recovery screen of a page whose stored page_doc cannot
// be read: it points to the versions (restore an earlier layout) and names the
// Details tab, where the page's own fields still edit.
func (a *Admin) renderBrokenDoc(w http.ResponseWriter, r *http.Request, e *entity.Entity, state docState) {
	id := e.ID.String()
	if state == docLegacy {
		a.ErrorBack(w, r, http.StatusConflict, "This page has a legacy layout",
			legacyLayoutMessage(e), a.adminPath+"/entities/"+id+"/versions", "Open the versions")
		return
	}
	a.ErrorBack(w, r, http.StatusUnprocessableEntity, "This page's layout cannot be read",
		"Its stored layout is damaged, so the builder cannot open it. Restore an earlier version from the versions list, or edit the page's other fields under Details.",
		a.adminPath+"/entities/"+id+"/versions", "Open the versions")
}

// legacyLayoutMessage explains a page whose page_sections cannot become a
// page_doc: the builder will not open it and nothing is written, so the public
// page stays as it is.
func legacyLayoutMessage(e *entity.Entity) string {
	_, err := entity.LegacyPageDocErr(e.Attributes)
	msg := "This page has a legacy layout that cannot be opened in the builder. Its content is intact and the public page is unchanged. To edit it here, its section ids must first be made valid (unique; letters, digits, - and _; at most 64 characters): an administrator can fix them in the stored sections or send a corrected page_doc through the API or MCP. Fixing ids changes the section id attributes in the public HTML. Entity " + e.ID.String()
	if err != nil {
		msg += " (" + err.Error() + ")"
	}
	return msg + "."
}

func (a *Admin) handlePageEditorPost(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadPageEntity(w, r)
	if !ok {
		return
	}
	if blogEditorFor(sc) {
		a.handleBlogSave(w, r, e, sc)
		return
	}
	if !pageEditorFor(sc, e) {
		Redirect(w, r, a.adminPath+"/entities/"+e.ID.String())
		return
	}
	doc, state := builderDoc(e)
	if state == docOK {
		a.handleBuilderPost(w, r, e, sc, doc)
		return
	}
	a.renderBrokenDoc(w, r, e, state)
}

// handlePreviewPost renders (or stages) a preview of the unsaved blog form. The
// builder stages its drafts through /builder/stage instead.
func (a *Admin) handlePreviewPost(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	if a.deps.Content.Preview == nil || !blogEditorFor(sc) {
		a.Error(w, r, http.StatusNotFound, "No preview", "The page preview is not available.")
		return
	}
	body, err := a.blogPreviewRequest(r, e, sc)
	if err != nil {
		a.serverError(w, r, "The preview could not be built", err)
		return
	}
	if r.Header.Get("X-HM-Stage") != "1" {
		// No-JavaScript "Preview": the form opens the rendered page in a new tab.
		a.servePreview(w, r, body)
		return
	}
	if len(body) > previewMaxBody {
		http.Error(w, "This page is too large to preview.", http.StatusRequestEntityTooLarge)
		return
	}
	tok, ok := a.mintPreview(r, e.ID, body)
	if !ok {
		a.refuse(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": a.adminPath + "/entities/" + e.ID.String() + "/edit/preview/" + url.PathEscape(tok)})
}

// handlePageRender applies the builder's ops to its posted doc and answers node
// fragments for the canvas. Nothing is stored.
func (a *Admin) handlePageRender(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadPageEntity(w, r)
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		a.Error(w, r, http.StatusUnsupportedMediaType, "No render", "Node rendering takes a JSON body.")
		return
	}
	a.handleBuilderRender(w, r, e, sc)
}

func cloneJSON(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
