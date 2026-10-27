package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// builderRenderBody is what the builder client posts to render: the doc as the
// browser holds it, plus the optional canvas patch inputs.
type builderRenderBody struct {
	Doc    json.RawMessage `json:"doc"`
	Ops    json.RawMessage `json:"ops"`
	Render json.RawMessage `json:"render"`
	CSS    json.RawMessage `json:"css"`
	Seq    json.RawMessage `json:"seq"`
}

// readBuilderBody reads a JSON body of at most previewMaxBody and checks that its
// doc parses. It answers the error itself and reports false.
func readBuilderBody(w http.ResponseWriter, r *http.Request) (*builderRenderBody, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, previewMaxBody)
	var b builderRenderBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeBuilderJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "This page is too large to render."})
			return nil, false
		}
		writeBuilderJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body."})
		return nil, false
	}
	if _, err := builder.Parse(b.Doc); err != nil {
		writeBuilderJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error(), "code": "invalid_page_doc"})
		return nil, false
	}
	return &b, true
}

func writeBuilderJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// builderBase is the render request for e without its layout: the stored
// attributes minus page_doc and page_sections (chrome and meta input only).
// The entity is always the one in the URL.
func (a *Admin) builderBase(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, canvas bool) map[string]any {
	attrs := map[string]json.RawMessage{}
	_ = json.Unmarshal(e.Attributes, &attrs)
	delete(attrs, "page_doc")
	delete(attrs, "page_sections")
	req := map[string]any{"entity_id": e.ID.String(), "schema": sc.Slug, "attributes": attrs, "canvas": canvas,
		"name": e.Name, "updated_at": e.UpdatedAt}
	if e.PublishedAt != nil {
		req["published_at"] = e.PublishedAt
	}
	if a.deps.Content.PublicPath != nil && e.Status == entity.StatusPublished {
		e.Schema = sc
		if pth := a.deps.Content.PublicPath(r.Context(), e); pth != "" {
			req["path"] = pth
		}
	}
	return req
}

// builderGate loads the entity and checks that the builder owns it.
func (a *Admin) builderGate(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.EntitySchema, bool) {
	e, sc, ok := a.loadPageEntity(w, r)
	if !ok {
		return nil, nil, false
	}
	if !pageEditorFor(sc, e) {
		a.Error(w, r, http.StatusNotFound, "No builder", "This entity is not edited in the builder.")
		return nil, nil, false
	}
	e.Schema = sc
	if _, state := builderDoc(e); state == docLegacy {
		a.Error(w, r, http.StatusConflict, "Legacy layout", legacyLayoutMessage(e))
		return nil, nil, false
	} else if state != docOK {
		a.Error(w, r, http.StatusNotFound, "No builder", "This page has no builder layout.")
		return nil, nil, false
	}
	return e, sc, true
}

// handleBuilderRender forwards the posted doc and ops to the node render
// endpoint (JSON mode). Nothing is stored.
func (a *Admin) handleBuilderRender(w http.ResponseWriter, r *http.Request, e *entity.Entity, sc *entity.EntitySchema) {
	if a.deps.Content.RenderNodes == nil {
		a.Error(w, r, http.StatusNotFound, "No render", "Node rendering is not available.")
		return
	}
	if !pageEditorFor(sc, e) {
		a.Error(w, r, http.StatusNotFound, "No builder", "This entity is not edited in the builder.")
		return
	}
	if _, state := builderDoc(e); state == docLegacy {
		a.Error(w, r, http.StatusConflict, "Legacy layout", legacyLayoutMessage(e))
		return
	} else if state != docOK {
		a.Error(w, r, http.StatusNotFound, "No builder", "This page has no builder layout.")
		return
	}
	b, ok := readBuilderBody(w, r)
	if !ok {
		return
	}
	req := a.builderBase(r, e, sc, true)
	req["doc"] = b.Doc
	for k, v := range map[string]json.RawMessage{"ops": b.Ops, "render": b.Render, "css": b.CSS, "seq": b.Seq} {
		if len(v) > 0 {
			req[k] = v
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		a.serverError(w, r, "The render request could not be built", err)
		return
	}
	serveJSON(a.deps.Content.RenderNodes, w, r, body)
}

// handleBuilderStage stages the posted doc as a canvas page and answers the
// frame URL.
func (a *Admin) handleBuilderStage(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	if a.deps.Content.Preview == nil {
		a.Error(w, r, http.StatusNotFound, "No preview", "The page preview is not available.")
		return
	}
	b, ok := readBuilderBody(w, r)
	if !ok {
		return
	}
	req := a.builderBase(r, e, sc, true)
	req["attributes"].(map[string]json.RawMessage)["page_doc"] = b.Doc
	body, err := json.Marshal(req)
	if err != nil {
		a.serverError(w, r, "The preview could not be built", err)
		return
	}
	if len(body) > previewMaxBody {
		writeBuilderJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "This page is too large to preview."})
		return
	}
	tok, ok := a.mintPreview(r, e.ID, body)
	if !ok {
		a.refuse(w, r)
		return
	}
	writeBuilderJSON(w, http.StatusOK, map[string]string{"url": a.adminPath + "/entities/" + e.ID.String() + "/edit/preview/" + url.PathEscape(tok)})
}
