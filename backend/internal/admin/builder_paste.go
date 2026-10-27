package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// pasteEnvelope is what the builder puts on the system clipboard when nodes
// are copied: {"hm":"nodes","v":2,"nodes":[...],"site":origin}. site is
// informational only and never trusted.
type pasteEnvelope struct {
	HM    string          `json:"hm"`
	V     int             `json:"v"`
	Nodes json.RawMessage `json:"nodes"` // parsed exactly (unknown fields refused, every digit kept) by builder.ParseExact
	Site  string          `json:"site"`
}

// pasteMaxBody bounds a paste request (same limit as a stored tree template).
const pasteMaxBody = treeTemplateMax

// handleBuilderPaste turns clipboard text into nodes safe to insert: the
// whole list must be a valid doc (nothing is dropped silently), without a
// layout-part slot, sanitised like a save, with fresh ids. It writes nothing.
// Context limits (loop.item binds outside a loop, only_in kinds, depth after
// insertion) are enforced by the client store ops and by the save validation.
func (a *Admin) handleBuilderPaste(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, pasteMaxBody)
	var req pasteEnvelope
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(&req)
	if err == nil && dec.More() {
		err = errors.New("trailing data")
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.templateProblem(w, http.StatusRequestEntityTooLarge, "too_large", "This is too large to paste.")
			return
		}
		a.templateProblem(w, http.StatusBadRequest, "bad_request", "Invalid request body.")
		return
	}
	if req.HM != "nodes" || req.V != 2 || len(req.Nodes) == 0 || string(req.Nodes) == "null" {
		a.templateProblem(w, http.StatusUnprocessableEntity, "not_nodes", "The clipboard does not hold copied elements.")
		return
	}
	doc, err := builder.ParseExact(append(append([]byte(`{"v":2,"nodes":`), req.Nodes...), '}'))
	if err != nil {
		a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_nodes", sentence(err.Error()))
		return
	}
	if len(doc.Nodes) == 0 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "not_nodes", "The clipboard does not hold copied elements.")
		return
	}
	if builder.SlotCount(doc) > 0 {
		a.templateProblem(w, http.StatusUnprocessableEntity, "part_slot", "The Content slot of a layout part cannot be pasted.")
		return
	}
	if a.entityRole(r.Context(), e) != rolePage {
		// a block or part is saved with the reference rules: refuse here what the save would refuse
		err := builder.CheckRefs(e.ID.String(), doc, func(id string) (*builder.Doc, bool) {
			return a.entityDoc(r.Context(), id, entity.BlockSchemaSlug)
		})
		if err != nil {
			a.templateProblem(w, http.StatusUnprocessableEntity, "ref_cycle", sentence(err.Error()))
			return
		}
	}
	entity.SanitizeDocComponents(doc)
	if err := entity.CheckSanitisedLength(doc); err != nil {
		a.templateProblem(w, http.StatusUnprocessableEntity, "invalid_page_doc", sentence(err.Error()))
		return
	}
	missing := []string{}
	for _, id := range builder.RefIDs(doc) {
		if _, ok := a.entityDoc(r.Context(), id, entity.BlockSchemaSlug); !ok {
			missing = append(missing, id)
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"nodes":         builder.FreshIDs(doc.Nodes, newNodeID),
		"missingBlocks": missing,
	})
}
