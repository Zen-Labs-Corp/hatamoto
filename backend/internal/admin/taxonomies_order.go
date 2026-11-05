package admin

import (
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/taxonomy"
)

func (a *Admin) handleTermMove(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(r, "id")
	if !ok || a.deps.Content.Terms == nil {
		a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
		return
	}
	t, err := a.deps.Content.Terms.GetTerm(r.Context(), id)
	if err != nil {
		a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
		return
	}
	back := backTo(r, a.adminPath+"/settings/taxonomies", a.adminPath+"/settings/taxonomies?kind="+t.Kind)
	nodes, err := a.termNodesOf(r.Context(), t.Kind)
	if err != nil {
		a.serverError(w, r, "The terms could not be loaded", err)
		return
	}
	on := make([]orderNode, 0, len(nodes))
	for _, n := range nodes {
		p := n.ParentID
		if n.Depth == 0 {
			p = nil
		}
		on = append(on, orderNode{ID: n.ID, Parent: p})
	}
	plan, can := planMove(r.PostFormValue("op"), on, id)
	if !can {
		a.SetFlash(w, r, "error", t.Name+" cannot move that way.")
		Redirect(w, r, back)
		return
	}
	res, err := a.deps.Content.Terms.MoveTerm(r.Context(), id, taxonomy.MoveInput{ParentID: plan.Parent, ClearParent: plan.Clear, Position: plan.Position})
	if err != nil {
		a.SetFlash(w, r, "error", "“"+t.Name+"” was not moved: "+sentence(err.Error()))
		Redirect(w, r, back)
		return
	}
	a.audit(r, "term.reorder", id.String(), map[string]any{"name": t.Name, "slug": t.Slug, "kind": t.Kind, "position": res.Position, "of": res.Total})
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", fmt.Sprintf("%s moved to position %d of %d.", t.Name, res.Position, res.Total))
	Redirect(w, r, back)
}
