package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// layoutNote is shown when a version holds a layout the diff cannot read.
const layoutNote = "The layout of one version cannot be read as a layout diff."

// layoutView is the "Layout changes" card of the compare screen.
type layoutView struct {
	Diffed      bool // both sides parsed: page_doc/page_sections leave the field table
	Note        string
	Rows        []layoutRow
	More        int // changes beyond the listed ones
	BaseVersion int // the version the restore forms vouch for
}

// layoutRow is one changed node.
type layoutRow struct {
	Name, Op, OpLabel, BadgeKind string
	Where                        string
	Detail                       []string
	RestoreAction, RestoreLabel  string // "" when the row cannot be restored
}

// layoutView builds the node diff of two versions' attributes. It is nil for
// a comparison that is not between two builder layouts (the flat table stays
// as it was) and carries only a note when a layout cannot be read.
func (a *Admin) layoutView(r *http.Request, e *entity.Entity, la, ra json.RawMessage, from, to int) *layoutView {
	ld, ls := builderDoc(&entity.Entity{Attributes: la})
	rd, rs := builderDoc(&entity.Entity{Attributes: ra})
	if ls == docNone || rs == docNone || ls == docLegacy || rs == docLegacy {
		return nil
	}
	if ls == docBroken || rs == docBroken {
		return &layoutView{Note: layoutNote}
	}
	fromN, toN := resolveVersion(e, from), resolveVersion(e, to)
	canRestore := fromN < toN && fromN != e.Version && a.mayRestoreNodes(r, e)

	diff := builder.DiffDocs(ld, rd)
	v := &layoutView{Diffed: true, More: diff.Truncated, BaseVersion: e.Version}
	for _, c := range diff.Changes {
		row := layoutRow{Name: c.Label, Op: c.Op, OpLabel: opLabel(c.Op), BadgeKind: opBadge(c.Op)}
		if c.Hint != "" {
			row.Name += " “" + c.Hint + "”"
		} else {
			row.Name += " (" + c.ID + ")"
		}
		switch c.Op {
		case builder.DiffAdded, builder.DiffRemoved:
			if c.Subtree > 0 {
				row.OpLabel += fmt.Sprintf(" (%d nodes)", c.Subtree+1)
			}
			row.Where = whereText(map[string]string{builder.DiffAdded: "in", builder.DiffRemoved: "from"}[c.Op], c)
		case builder.DiffMoved:
			row.Where = whereText("to", c)
		}
		row.Detail = fieldDetails(c)
		if canRestore && c.Op != builder.DiffAdded {
			row.RestoreAction = a.adminPath + fmt.Sprintf("/entities/%s/versions/%d/nodes/%s/restore", e.ID, fromN, url.PathEscape(c.ID))
			row.RestoreLabel = fmt.Sprintf("Restore this node from v%d", fromN)
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

// mayRestoreNodes reports whether the restore forms should be offered: the
// person can edit and the current page is one the builder owns.
func (a *Admin) mayRestoreNodes(r *http.Request, e *entity.Entity) bool {
	u := userFrom(r.Context())
	if u == nil || (u.Role != "owner" && u.Role != "editor") {
		return false
	}
	sc := e.Schema
	if sc == nil {
		var err error
		if sc, err = a.deps.Content.Entities.GetSchema(r.Context(), e.SchemaID); err != nil {
			return false
		}
	}
	_, state := builderDoc(e)
	return pageEditorFor(sc, e) && state == docOK
}

// resolveVersion maps the compare screen's 0 ("current") to the version number.
func resolveVersion(e *entity.Entity, n int) int {
	if n == 0 {
		return e.Version
	}
	return n
}

func opLabel(op string) string {
	switch op {
	case builder.DiffAdded:
		return "Added"
	case builder.DiffRemoved:
		return "Removed"
	case builder.DiffMoved:
		return "Moved"
	}
	return "Changed"
}

func opBadge(op string) string {
	switch op {
	case builder.DiffAdded:
		return "success"
	case builder.DiffRemoved:
		return "danger"
	case builder.DiffMoved:
		return "info"
	}
	return "warning"
}

// whereText says where a node is: "in the top level, position 3".
func whereText(prep string, c builder.NodeChange) string {
	place := "the top level"
	if c.Parent != "" {
		place = "“" + c.Parent + "”"
	}
	return fmt.Sprintf("%s %s, position %d", prep, place, c.Index+1)
}

// fieldDetails lists what changed inside a node as short sentences.
func fieldDetails(c builder.NodeChange) []string {
	var out []string
	if c.TypeFrom != "" {
		out = append(out, fmt.Sprintf("Type: %s → %s", c.TypeFrom, c.T))
	}
	add := func(prefix string, fs []builder.FieldChange) {
		for _, f := range fs {
			name := prefix + f.Key
			if prefix == "style " {
				name = prefix + styleKeyTitle(f.Key)
			}
			if f.BP != "" {
				bp := f.BP
				if prefix == "style " {
					bp = breakpointTitle(bp)
				}
				name = fmt.Sprintf("%s (%s)", name, bp)
			}
			var line string
			switch f.Kind {
			case builder.DiffAdded:
				line = fmt.Sprintf("%s: set to %s", name, quote(f.New))
			case builder.DiffRemoved:
				line = fmt.Sprintf("%s: cleared (was %s)", name, quote(f.Old))
			default:
				line = fmt.Sprintf("%s: %s → %s", name, quote(f.Old), quote(f.New))
			}
			if f.Note != "" {
				line += " (" + f.Note + ")"
			}
			out = append(out, line)
		}
	}
	add("", c.Props)
	add("style ", c.Styles)
	add("data ", c.Binds)
	return out
}

func quote(s string) string { return "“" + s + "”" }

// dropPrefixes removes the field rows whose path is one of names or below it.
func dropPrefixes(rows []diffRow, names ...string) []diffRow {
	out := rows[:0:0]
rows:
	for _, r := range rows {
		for _, n := range names {
			if r.Path == n || strings.HasPrefix(r.Path, n+".") {
				continue rows
			}
		}
		out = append(out, r)
	}
	return out
}

// Why a node cannot be put back.
var (
	errNodeGone  = errors.New("that node is not in the version")
	errIDCollide = errors.New("an id of the restored node is used by a different node")
)

// restoreNode puts node id of old into cur. A node cur still holds gets its
// own fields (kind, props, styles, bindings) back and keeps the children it
// has now; one that moved is also returned next to the siblings it had. A node
// that was removed goes back with its whole old subtree the same way. When the
// old parent is gone the node is put at the end of the top level and note says
// so. A subtree id that a different node of cur uses is errIDCollide and cur is
// left as it was.
func restoreNode(cur, old *builder.Doc, id string) (note string, err error) {
	src, oldParent := old.Find(id)
	if src == nil {
		return "", errNodeGone
	}
	oldSibs := siblings(old, oldParent)
	oldIndex := indexIn(oldSibs, id)
	parentID := ""
	if oldParent != nil {
		parentID = oldParent.ID
	}

	var node builder.Node
	if err := copyViaJSON(src, &node); err != nil {
		return "", err
	}
	// Work on a copy so a refusal leaves cur untouched.
	var work builder.Doc
	if err := copyViaJSON(cur, &work); err != nil {
		return "", err
	}

	existing, _ := work.Find(id)
	if existing != nil {
		// The node is still in the page: put back its own fields and keep the
		// children it has now (their changes are rows of their own).
		node.C = existing.C
		*existing = node
		moved := false
		for _, c := range builder.DiffDocs(old, cur).Changes {
			if c.ID == id && c.Op == builder.DiffMoved {
				moved = true
			}
		}
		if moved {
			removeNode(&work, id)
			note = placeNode(&work, &node, parentID, oldSibs, oldIndex)
		}
		*cur = work
		return note, nil
	}
	if used := usedID(&work, &node); used != "" {
		return "", fmt.Errorf("%w (%s)", errIDCollide, used)
	}
	note = placeNode(&work, &node, parentID, oldSibs, oldIndex)
	*cur = work
	return note, nil
}

// placeNode inserts n under the node parentID (the top level for "") next to
// the siblings it had; a parent that is gone sends it to the end of the top
// level and the returned note says so.
func placeNode(work *builder.Doc, n *builder.Node, parentID string, oldSibs []*builder.Node, oldIndex int) (note string) {
	parent, _ := work.Find(parentID)
	if parentID != "" && parent == nil {
		insertAt(work, nil, len(work.Nodes), n)
		return "Its old parent is gone, so it was put at the end of the top level."
	}
	insertAt(work, parent, placeIndex(siblings(work, parent), oldSibs, oldIndex), n)
	return ""
}

func copyViaJSON(from, to any) error {
	raw, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, to)
}

// placeIndex is where in list a node goes that had position oldIndex among
// oldSibs: right after the nearest earlier sibling list still has, else right
// before the nearest later one, else its old index.
func placeIndex(list, oldSibs []*builder.Node, oldIndex int) int {
	for i := oldIndex - 1; i >= 0; i-- {
		if at := indexIn(list, oldSibs[i].ID); at < len(list) {
			return at + 1
		}
	}
	for i := oldIndex + 1; i < len(oldSibs); i++ {
		if at := indexIn(list, oldSibs[i].ID); at < len(list) {
			return at
		}
	}
	return oldIndex
}

func siblings(d *builder.Doc, parent *builder.Node) []*builder.Node {
	if parent == nil {
		return d.Nodes
	}
	return parent.C
}

func indexIn(ns []*builder.Node, id string) int {
	for i, n := range ns {
		if n.ID == id {
			return i
		}
	}
	return len(ns)
}

// removeNode takes the node with id out of d.
func removeNode(d *builder.Doc, id string) {
	var strip func(ns []*builder.Node) []*builder.Node
	strip = func(ns []*builder.Node) []*builder.Node {
		out := ns[:0:0]
		for _, n := range ns {
			if n.ID == id {
				continue
			}
			n.C = strip(n.C)
			out = append(out, n)
		}
		return out
	}
	d.Nodes = strip(d.Nodes)
}

// insertAt puts n into parent's children (the top level for nil) at index i,
// clamped to the end.
func insertAt(d *builder.Doc, parent *builder.Node, i int, n *builder.Node) {
	list := &d.Nodes
	if parent != nil {
		list = &parent.C
	}
	i = min(max(i, 0), len(*list))
	*list = append(*list, nil)
	copy((*list)[i+1:], (*list)[i:])
	(*list)[i] = n
}

func walkIDs(n *builder.Node, fn func(id string) bool) bool {
	if !fn(n.ID) {
		return false
	}
	for _, c := range n.C {
		if !walkIDs(c, fn) {
			return false
		}
	}
	return true
}

// usedID returns the first id of node's subtree that d already holds ("" none).
func usedID(d *builder.Doc, n *builder.Node) string {
	hit := ""
	walkIDs(n, func(id string) bool {
		if f, _ := d.Find(id); f != nil {
			hit = id
			return false
		}
		return true
	})
	return hit
}

// handleVersionRestoreNode puts one node of an earlier version back into the
// current page. The form carries base_version, the version the comparison was
// opened at; nothing is written when the page moved on since.
func (a *Admin) handleVersionRestoreNode(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	id := e.ID.String()
	versions := a.adminPath + "/entities/" + id + "/versions"
	if base, _ := strconv.Atoi(r.FormValue("base_version")); base != e.Version {
		a.ErrorBack(w, r, http.StatusConflict, "This page changed since you opened the comparison",
			"Nothing was restored. Open the comparison again to see the current page.", versions, "Back to versions")
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 || n >= e.Version {
		a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "That version cannot be restored from.", versions, "Back to versions")
		return
	}
	v, err := a.deps.Content.Entities.GetVersion(r.Context(), e.ID, n)
	if err != nil {
		if errors.Is(err, entity.ErrVersionNotFound) {
			a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "That version no longer exists.", versions, "Back to versions")
			return
		}
		a.serverError(w, r, "The version could not be loaded", err)
		return
	}
	old, state := builderDoc(&entity.Entity{Attributes: v.Attributes})
	cur, curState := builderDoc(e)
	nodeID := r.PathValue("node")
	if state != docOK || curState != docOK || nodeID == "" {
		a.ErrorBack(w, r, http.StatusNotFound, "Node not found", "That version has no layout to restore from.", versions, "Back to versions")
		return
	}
	src, _ := old.Find(nodeID)
	if src == nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Node not found", "That node is not in version "+strconv.Itoa(n)+".", versions, "Back to versions")
		return
	}
	name := nodeName(src)
	note, err := restoreNode(cur, old, nodeID)
	if err != nil {
		if errors.Is(err, errIDCollide) {
			a.SetFlash(w, r, "error", "That node cannot be restored: "+err.Error()+". Nothing was changed.")
			Redirect(w, r, versions)
			return
		}
		a.serverError(w, r, "The node could not be restored", err)
		return
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		a.serverError(w, r, "The node could not be restored", err)
		return
	}
	summary := fmt.Sprintf("Restored node “%s” from version %d", name, n)
	if _, err := a.saveBuilderDocAs(r.Context(), actorID(r), e, raw, opSave, nil, summary); err != nil {
		var p *saveProblem
		if errors.As(err, &p) {
			a.ErrorBack(w, r, p.Status, "The node could not be restored", p.Msg, versions, "Back to versions")
			return
		}
		a.serverError(w, r, "The node could not be restored", err)
		return
	}
	msg := fmt.Sprintf("Restored “%s” from version %d.", name, n)
	if note != "" {
		msg += " " + note
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, a.adminPath+"/entities/"+id+"/edit")
}

// nodeName is how a node is called in messages: its text, else its id.
func nodeName(n *builder.Node) string {
	for _, k := range []string{"navlabel", "text", "title", "label", "alt", "name"} {
		if s, ok := n.P[k].(string); ok && strings.TrimSpace(s) != "" {
			r := []rune(strings.TrimSpace(s))
			if len(r) > 40 {
				return string(r[:40]) + "…"
			}
			return string(r)
		}
	}
	if s, ok := n.P["html"].(string); ok && (n.T == "heading" || n.T == "text") {
		if r := []rune(strings.TrimSpace(builder.InlineText(s))); len(r) > 0 {
			if len(r) > 40 {
				return string(r[:40]) + "…"
			}
			return string(r)
		}
	}
	return n.ID
}
