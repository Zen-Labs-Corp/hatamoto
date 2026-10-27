package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/sectiontemplate"
)

// Tree templates live in the section-template table: a row whose section_type
// is treeTemplateType holds {"v":1,"nodes":[...]}. Rows of any other type are
// the older section templates; they insert as one component node.
const (
	treeTemplateType = "hm-tree"
	// treeTemplateMax is the stored content limit (the store's own is the same).
	treeTemplateMax = 512 * 1024
	// templateListMax bounds the template list the palette carries.
	templateListMax = 100
)

func (a *Admin) mountBuilderTemplates(w *httpx.Group) {
	w.HandleFunc("POST /entities/{id}/builder/template", a.handleSaveTemplate)
	w.HandleFunc("GET /builder/templates/{id}", a.handleTemplateNodes)
	w.HandleFunc("POST /entities/{id}/builder/paste", a.handleBuilderPaste)
}

type saveTemplateRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	NodeID      string          `json:"nodeId"`
	Page        bool            `json:"page"`
	Doc         json.RawMessage `json:"doc"`
}

func (a *Admin) templateProblem(w http.ResponseWriter, status int, code, msg string) {
	a.writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

// handleSaveTemplate stores a subtree (nodeId) or the whole page of the posted
// doc as a tree template. Nothing is stored unless the nodes are a valid doc.
func (a *Admin) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	ts := a.deps.Content.Templates
	if ts == nil {
		a.templateProblem(w, http.StatusNotFound, "unavailable", "Templates are not available on this site.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, builderSaveMaxBody)
	var req saveTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.templateProblem(w, http.StatusBadRequest, "bad_request", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 255 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_name", "Name the template, using at most 255 characters.")
		return
	}
	if len([]rune(req.Description)) > 1000 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_description", "Use at most 1000 characters for the description.")
		return
	}
	doc := (*builder.Doc)(nil)
	if len(req.Doc) > 0 {
		var err error
		if doc, err = builder.Parse(req.Doc); err != nil {
			a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_page_doc", sentence(err.Error()))
			return
		}
	} else {
		doc, _ = builderDoc(e)
	}
	src := doc.Nodes
	if req.NodeID != "" && !req.Page {
		n, _ := doc.Find(req.NodeID)
		if n == nil {
			a.templateProblem(w, http.StatusUnprocessableEntity, "unknown_node", "That element is not in the page.")
			return
		}
		src = []*builder.Node{n}
	}
	if len(src) == 0 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "empty", "There is nothing to save as a template yet.")
		return
	}
	if raw, err := json.Marshal(&builder.Doc{V: 2, Nodes: src}); err != nil {
		a.serverError(w, r, "The template could not be built", err)
		return
	} else if _, err := builder.Parse(raw); err != nil {
		a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_page_doc", sentence(err.Error()))
		return
	}
	if builder.SlotCount(&builder.Doc{Nodes: src}) > 0 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "part_slot", "A template cannot hold the Content slot of a layout part.")
		return
	}
	content, err := json.Marshal(map[string]any{"v": 1, "nodes": src})
	if err != nil {
		a.serverError(w, r, "The template could not be built", err)
		return
	}
	if len(content) > treeTemplateMax {
		a.templateProblem(w, http.StatusUnprocessableEntity, "too_large", "This is too large to save as a template.")
		return
	}
	tpl, err := ts.Create(r.Context(), actorID(r), &sectiontemplate.CreateInput{
		Name: name, Description: strings.TrimSpace(req.Description), SectionType: treeTemplateType, Content: content,
	})
	if err != nil {
		var ve *sectiontemplate.ValidationError
		if errors.As(err, &ve) {
			a.templateProblem(w, http.StatusUnprocessableEntity, "invalid", sentence(ve.Message))
			return
		}
		a.logger().Error("admin: save template failed", "err", err)
		a.templateProblem(w, http.StatusInternalServerError, "server_error", "The template could not be saved.")
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"id": tpl.ID.String(), "name": tpl.Name})
}

// handleTemplateNodes returns the nodes to insert for a template, with fresh
// ids on every call. A tree template skips the top-level nodes this build
// cannot read; an older section template is one component node.
func (a *Admin) handleTemplateNodes(w http.ResponseWriter, r *http.Request) {
	ts := a.deps.Content.Templates
	id, err := uuid.Parse(r.PathValue("id"))
	if ts == nil || err != nil {
		a.templateProblem(w, http.StatusNotFound, "not_found", "No such template.")
		return
	}
	t, err := ts.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
			a.templateProblem(w, http.StatusNotFound, "not_found", "No such template.")
			return
		}
		a.logger().Error("admin: load template failed", "err", err)
		a.templateProblem(w, http.StatusInternalServerError, "server_error", "The template could not be loaded.")
		return
	}
	var nodes []*builder.Node
	kind := "section"
	if t.SectionType == treeTemplateType {
		kind = "tree"
		nodes = builder.ReadableNodes(t.Content)
		if len(nodes) == 0 {
			a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_template", "This template has nothing this version can insert.")
			return
		}
	} else {
		byType, _ := a.themeTypes()
		if _, ok := byType[t.SectionType]; !ok {
			a.templateProblem(w, http.StatusUnprocessableEntity, "section_type", "The section type “"+t.SectionType+"” is not in this theme.")
			return
		}
		var content map[string]any
		if json.Unmarshal(t.Content, &content) != nil || content == nil {
			content = map[string]any{}
		}
		nodes = []*builder.Node{{ID: newNodeID(), T: "component", P: map[string]any{"type": t.SectionType, "content": content}}}
		if raw, err := json.Marshal(&builder.Doc{V: 2, Nodes: nodes}); err != nil {
			a.serverError(w, r, "The template could not be built", err)
			return
		} else if _, err := builder.Parse(raw); err != nil {
			a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_template", sentence(err.Error()))
			return
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"id": t.ID.String(), "name": t.Name, "kind": kind, "nodes": builder.FreshIDs(nodes, newNodeID)})
}

type templateItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
}

// templateItems lists the templates the palette offers: tree templates and the
// older section templates whose type the theme has.
func (a *Admin) templateItems(r *http.Request) []templateItem {
	out := []templateItem{}
	ts := a.deps.Content.Templates
	if ts == nil {
		return out
	}
	res, err := ts.List(r.Context(), &sectiontemplate.ListFilter{}, templateListMax, 0)
	if err != nil || res == nil {
		return out
	}
	byType, _ := a.themeTypes()
	for _, t := range res.Items {
		kind := "tree"
		if t.SectionType != treeTemplateType {
			if _, ok := byType[t.SectionType]; !ok {
				continue
			}
			kind = "section"
		}
		out = append(out, templateItem{ID: t.ID.String(), Name: t.Name, Description: t.Description, Kind: kind})
	}
	return out
}
