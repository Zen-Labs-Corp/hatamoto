package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// Limits of one POST /edit/render request.
const (
	maxRenderOps = 100
	maxRenderIDs = 50
)

// renderNodesRequest is the body of the node render endpoint: the draft
// attributes (as the preview takes them), builder ops applied to the draft doc
// in memory, and the node ids to render or compile styles for.
type renderNodesRequest struct {
	previewRequest
	// Doc, when set, is the draft doc itself and replaces attributes.page_doc;
	// attributes then only feed the site chrome and meta.
	Doc    json.RawMessage `json:"doc"`
	Ops    []builder.Op    `json:"ops"`
	Render []string        `json:"render"`
	CSS    []string        `json:"css"`
	Seq    int             `json:"seq"`
}

// renderNodesResponse carries the node fragments of a canvas patch. Order is
// the top-level ids the draft renders, in order, so the client can verify its
// canvas against the server's truth.
type renderNodesResponse struct {
	Fragments []render.Fragment `json:"fragments"`
	CSS       map[string]string `json:"css"`
	Order     []string          `json:"order"`
	Seq       int               `json:"seq"`
}

// handleRenderNodes applies ops to the draft doc in the request and renders the
// asked nodes as fragments. Stateless: the draft lives and dies with the
// request, and no full page is ever rendered.
func (h *renderAdminHandlers) handleRenderNodes(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPreviewBodySize)
	var req renderNodesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Ops) > maxRenderOps || len(req.Render) > maxRenderIDs || len(req.CSS) > maxRenderIDs {
		writeError(w, http.StatusBadRequest, "too many ops or ids")
		return
	}

	var doc *builder.Doc
	if len(req.Doc) > 0 && string(req.Doc) != "null" {
		var err error
		if doc, err = builder.Parse(req.Doc); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error(), "code": "invalid_page_doc"})
			return
		}
	} else if doc = docFromAttributes(req.Attributes, req.EntityID); doc == nil {
		// The draft doc is read from the attributes, not from the view: a doc
		// that is only v1 sections reaches the view as sections.
		writeError(w, http.StatusConflict, "no_doc")
		return
	}
	v, _, err := h.previewView(r, &req.previewRequest)
	if err != nil {
		h.logger.Error("render nodes: site chrome", "error", err)
		writeError(w, http.StatusInternalServerError, "render failed")
		return
	}
	for _, op := range req.Ops {
		if _, err := doc.Apply(op); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if err := doc.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	entity.SanitizeDocComponents(doc, nil)
	if err := entity.CheckSanitisedLength(doc); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	v.Doc, v.Preview, v.Canvas = doc, true, true
	v.PluginDirectory, v.ThemeDirectory = h.directoryFor(r.Context(), v.Sections, doc)

	out := renderNodesResponse{Fragments: []render.Fragment{}, CSS: map[string]string{}, Order: []string{}, Seq: req.Seq}
	// A node under a loop cannot render without its item scope: render the loop.
	req.Render = loopHosts(doc, req.Render)
	wanted := make(map[string]bool, len(req.Render))
	for _, id := range req.Render {
		wanted[id] = true
	}
	// One ext batch for the whole doc, as a page render does.
	h.engine.PrefetchExt(v, doc.Nodes)
	loopsBefore := make(map[string]int, len(doc.Nodes)) // the loop count when each top node started
	for _, n := range doc.Nodes {
		loopsBefore[n.ID] = v.Loops()
		f, ok, err := h.engine.RenderTop(v, n)
		if err != nil {
			h.logger.Error("render nodes: render", "node", n.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "render failed")
			return
		}
		if !ok {
			continue
		}
		out.Order = append(out.Order, f.Key)
		if wanted[f.Key] || wanted[n.ID] {
			out.Fragments = append(out.Fragments, f)
			delete(wanted, f.Key)
			delete(wanted, n.ID)
		}
	}
	// Nested nodes (not top level) render through RenderNode.
	for _, id := range req.Render {
		if !wanted[id] {
			continue
		}
		n, parent := doc.Find(id)
		if n == nil || parent == nil {
			continue
		}
		// the first pass already counted this node's loops: count them again from the top node's start
		for _, top := range doc.Nodes {
			if in, _ := (&builder.Doc{Nodes: []*builder.Node{top}}).Find(id); in != nil {
				v.SetLoops(loopsBefore[top.ID])
				break
			}
		}
		var b bytes.Buffer
		if err := h.engine.RenderNode(&b, v, n, true); err != nil {
			h.logger.Error("render nodes: render", "node", id, "error", err)
			writeError(w, http.StatusInternalServerError, "render failed")
			return
		}
		if b.Len() > 0 {
			out.Fragments = append(out.Fragments, render.Fragment{Sel: "node", Key: id, HTML: b.String(), CSS: h.engine.NodeCSSFor(v, n, parent)})
		}
	}
	for _, id := range req.CSS {
		if n, parent := doc.Find(id); n != nil {
			for k, rules := range h.engine.NodeCSSFor(v, n, parent) {
				out.CSS[k] = rules
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// loopHosts maps each id to its outermost loop ancestor (itself when none), without duplicates.
func loopHosts(doc *builder.Doc, ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if host := loopAncestor(doc, id); host != "" {
			id = host
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// loopAncestor is the id of the outermost loop strictly above id, or "".
func loopAncestor(doc *builder.Doc, id string) string {
	var walk func(ns []*builder.Node, loop string) (string, bool)
	walk = func(ns []*builder.Node, loop string) (string, bool) {
		for _, n := range ns {
			if n.ID == id {
				return loop, true
			}
			next := loop
			if next == "" && n.T == "loop" {
				next = n.ID
			}
			if host, ok := walk(n.C, next); ok {
				return host, true
			}
		}
		return "", false
	}
	host, _ := walk(doc.Nodes, "")
	return host
}
