package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/urlpattern"
)

// orderData feeds entities/order.
type orderData struct {
	Title, Description string
	Type, Back, Action string
	Nested             bool
	Table              ui.Table
	Locale             string
	DepthNote          string
	Actions            []ui.Button
}

// orderLine is one row of the tree in display order.
type orderLine struct {
	ID, Name, Status string
	Depth            int
}

// orderTree loads the rows to order for a schema: the default language's
// (or the requested locale's) top level and nested rows in tree order.
func orderTree(rows []*entity.Entity) ([]orderNode, map[uuid.UUID]*entity.Entity, []orderLine, map[uuid.UUID]int) {
	by := map[uuid.UUID]*entity.Entity{}
	nodes := make([]orderNode, 0, len(rows))
	kids := map[uuid.UUID][]*entity.Entity{}
	var roots []*entity.Entity
	for _, e := range rows {
		by[e.ID] = e
	}
	for _, e := range rows {
		if e.ParentID != nil && by[*e.ParentID] != nil {
			kids[*e.ParentID] = append(kids[*e.ParentID], e)
		} else {
			roots = append(roots, e)
		}
	}
	depth := map[uuid.UUID]int{}
	var out []orderLine
	var walk func(e *entity.Entity, d int)
	walk = func(e *entity.Entity, d int) {
		if _, seen := depth[e.ID]; seen {
			return
		}
		depth[e.ID] = d
		var parent *uuid.UUID
		if d > 0 {
			parent = e.ParentID
		}
		nodes = append(nodes, orderNode{ID: e.ID, Parent: parent})
		out = append(out, orderLine{ID: e.ID.String(), Name: e.Name, Status: string(e.Status), Depth: d})
		for _, c := range kids[e.ID] {
			walk(c, d+1)
		}
	}
	for _, e := range roots {
		walk(e, 0)
	}
	return nodes, by, out, depth
}

func (a *Admin) orderLocale(r *http.Request, sc *entity.EntitySchema) string {
	if l := r.FormValue("locale"); l != "" {
		return l
	}
	if a.deps.Site != nil {
		if cfg, err := a.loadSite(r.Context()); err == nil && cfg.Language != "" {
			return cfg.Language
		}
	}
	return "en"
}

// schemaOf finds the orderable schema with this slug.
func (a *Admin) orderSchema(r *http.Request, slug string) *entity.EntitySchema {
	schemas, err := a.deps.Content.Entities.ListSchemas(r.Context())
	if err != nil {
		return nil
	}
	for _, s := range schemas {
		if s.Slug == slug && !entity.IsReuseSchema(s) {
			return s
		}
	}
	return nil
}

func (a *Admin) handleEntityOrder(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	slug := r.URL.Query().Get("type")
	sc := a.orderSchema(r, slug)
	if sc == nil {
		a.notFound(w, r, "Entity type", a.adminPath+"/entities", "Back to entities")
		return
	}
	locale := a.orderLocale(r, sc)
	rows, err := a.deps.Content.Entities.OrderRows(r.Context(), sc.ID, locale)
	if err != nil {
		a.serverError(w, r, "The entries could not be loaded", err)
		return
	}
	nested := schemaIsNested(sc)
	if !nested {
		for _, e := range rows {
			e.ParentID = nil
		}
	}
	nodes, _, list, depth := orderTree(rows)
	d := orderData{
		Title: "Reorder " + typePluralFor(sc.Slug, sc.Name), Type: sc.Slug, Nested: nested, Locale: locale,
		Back:   a.adminPath + "/entities?type=" + url.QueryEscape(sc.Slug),
		Action: a.adminPath + "/entities/",
	}
	d.Actions = []ui.Button{{Label: "Back to the list", Href: d.Back}}
	d.Description = "Use the buttons to move an entry up or down among its siblings."
	if nested {
		d.Description = "Use the buttons to move an entry up, down, in under the one above or out to its parent's level."
		d.DepthNote = fmt.Sprintf("Depth limit: %d levels in the URL", urlpattern.MaxPathDepth)
	}
	t := ui.Table{ID: "order", Caption: "Order of " + typePluralFor(sc.Slug, sc.Name), BaseURL: d.Back, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "status", Label: "Status"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "Nothing to order yet", Body: "Create an entry first."}}
	self := a.adminPath + "/entities/order?type=" + url.QueryEscape(sc.Slug)
	for i, row := range list {
		id := nodes[i].ID
		act := ui.Row{ID: row.ID, Label: row.Name, Cells: []ui.Cell{{Text: row.Name, Depth: min(row.Depth, 7)}, {Text: row.Status}}}
		for _, op := range moveOps {
			if !nested && (op.Op == "indent" || op.Op == "outdent") {
				continue
			}
			if _, can := planMove(op.Op, nodes, id); !can {
				continue
			}
			if op.Op == "indent" && depth[id]+1+a.subtreeHeight(nodes, id) >= urlpattern.MaxPathDepth {
				continue
			}
			act.Actions = append(act.Actions, ui.Action{Label: op.Label, Post: d.Action + row.ID + "/move",
				Fields: map[string]string{"op": op.Op, "back": self}})
		}
		t.Rows = append(t.Rows, act)
	}
	d.Table = t
	v := a.NewView(w, r, d.Title)
	v.Data = d
	a.Render(w, r, http.StatusOK, "entities/order", v)
}

// subtreeHeight is how many levels hang below id in nodes (display order).
func (a *Admin) subtreeHeight(nodes []orderNode, id uuid.UUID) int {
	kids := map[uuid.UUID][]uuid.UUID{}
	for _, n := range nodes {
		if n.Parent != nil {
			kids[*n.Parent] = append(kids[*n.Parent], n.ID)
		}
	}
	var h func(x uuid.UUID, guard int) int
	h = func(x uuid.UUID, guard int) int {
		best := 0
		if guard > urlpattern.MaxPathDepth+1 {
			return 0
		}
		for _, c := range kids[x] {
			best = max(best, 1+h(c, guard+1))
		}
		return best
	}
	return h(id, 0)
}

func (a *Admin) handleEntityMove(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	e, err := c.Entities.GetEntity(r.Context(), id)
	if err != nil || e.Schema == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	fallback := a.adminPath + "/entities/order?type=" + url.QueryEscape(e.Schema.Slug)
	back := backTo(r, a.adminPath+"/entities", fallback)
	fail := func(msg string) {
		a.SetFlash(w, r, "error", msg)
		Redirect(w, r, back)
	}
	var in entity.MoveInput
	if op := r.PostFormValue("op"); op != "" {
		rows, err := c.Entities.OrderRows(r.Context(), e.SchemaID, e.Locale)
		if err != nil {
			a.serverError(w, r, "The entries could not be loaded", err)
			return
		}
		if !schemaIsNested(e.Schema) {
			for _, x := range rows {
				x.ParentID = nil
			}
		}
		nodes, _, _, _ := orderTree(rows)
		plan, can := planMove(op, nodes, id)
		if !can {
			fail(e.Name + " cannot move that way.")
			return
		}
		in = entity.MoveInput{ParentID: plan.Parent, ClearParent: plan.Clear, Position: plan.Position}
	} else {
		// The drag enhancement posts a position and optionally a parent.
		in.Position, _ = strconv.Atoi(r.PostFormValue("position"))
		if p := r.PostFormValue("parent"); p == "none" {
			in.ClearParent = true
		} else if pid, err := uuid.Parse(p); err == nil {
			in.ParentID = &pid
		}
	}
	res, err := c.Entities.MoveEntity(r.Context(), id, actorID(r), in)
	if err != nil {
		fail("“" + e.Name + "” was not moved: " + entityErrorText(err))
		return
	}
	detail := map[string]any{"position": res.Position, "of": res.Total}
	if res.Entity.ParentID != nil {
		detail["parent"] = res.Entity.ParentID.String()
	}
	a.auditEntity(r, auditEntityReorder, id, res.Entity, detail)
	a.invalidate(r.Context())
	msg := fmt.Sprintf("%s moved to position %d of %d", e.Name, res.Position, res.Total)
	if op := r.PostFormValue("op"); op == "indent" || op == "outdent" {
		msg += " at its new level"
	}
	a.SetFlash(w, r, "success", msg+".")
	if r.PostFormValue("op") != "" {
		back += "#row-" + id.String() // the page puts focus back on the moved row
	}
	Redirect(w, r, back)
}

// termMoveAction is a Move up/down, Indent or Outdent row action of the term table.
func (a *Admin) termMoveAction(label, op, id, kind string) ui.Action {
	return ui.Action{Label: label, Post: a.adminPath + "/settings/taxonomies/terms/" + id + "/move",
		Fields: map[string]string{"op": op, "back": a.adminPath + "/settings/taxonomies?kind=" + url.QueryEscape(kind)}}
}
