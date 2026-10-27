package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// The no-JS outline: the page's node tree as one nested form. Like the classic
// editor it is stateless: every post carries the working doc, applies the typed
// values to it, then the requested operation. Component content goes through
// decodeComponent, native props through builder.CoerceProp and Doc.Apply, the
// same validators the JS inspector's endpoints reach.

// outlineSkipProp lists props the outline never edits: the v1 marker is
// structural, the component type is its identity.
var outlineSkipProp = map[string]bool{"v1": true, "type": true}

type outlineOption struct {
	Value, Label string
	Selected     bool
}

type outlineProp struct {
	ID, Name, Label string
	Control, Type   string // hm/field control (input, textarea, select, checkbox) and input type
	Value           string
	Checked         bool
	Options         []outlineOption
	Attrs           map[string]string
	Error           string
	Help            string
	// Bind is the "bind to" control of a bindable prop; nil for the others.
	Bind *outlineBind
}

// outlineBind is the second control of a bindable prop: a select of the
// sources it may take ("" = the literal value) and, for an attribute source,
// the path typed after its prefix.
type outlineBind struct {
	ID, Name, Label             string
	Options                     []outlineOption
	PathID, PathName, PathValue string
	ShowPath                    bool
}

type outlineNode struct {
	ID, Kind, Legend string
	Props            []outlineProp
	Form             template.HTML // component content fields
	Unknown          bool          // a component whose type the theme lacks
	Orphan           bool          // a plugin widget that is not installed (no props to edit)
	CanNest          bool
	First, Last      bool
	Children         []*outlineNode
	Errors           []string
}

type outlineData struct {
	ID, Name, Status, Version string
	Doc                       string // the working doc, carried in the form
	Outline                   bool   // the page asked for the outline (?outline=1), so a save returns to it
	Nodes                     []*outlineNode
	Kinds                     []outlineOption
	Plain                     bool // rendered as the page itself, not inside noscript
	Published, CanPublish     bool
	DetailsHref               string
	Notice, Conflict          string
	Summary                   []ui.FieldError
	Shell                     *builderShell // the JS shell's markup data; nil in the outline's own responses
}

// newNodeID is a fresh node id, shaped like the ones the browser makes.
func newNodeID() string {
	u := uuid.New()
	return fmt.Sprintf("n%x", u[:5])
}

// outlineKinds are the kinds the outline can add: native kinds, then one
// "component:<type>" per theme section type.
func (a *Admin) outlineKinds() []outlineOption {
	var out []outlineOption
	for _, k := range builder.Kinds() {
		if k.Kind != "component" && !k.Hidden {
			label := k.Label
			if label == "" {
				label = kindTitle(k.Kind, nil)
			}
			out = append(out, outlineOption{Value: k.Kind, Label: label})
		}
	}
	_, list := a.themeTypes()
	for _, t := range list {
		out = append(out, outlineOption{Value: "component:" + t.Type, Label: "Section: " + t.Title})
	}
	return out
}

// builderKindNames are the plain names of the kinds Humanize would run together
// ("Accorditem"); js/builder/kindnames.js (FIX) says the same.
var builderKindNames = map[string]string{"accorditem": "Accordion item", "tabpanel": "Tab panel", "loopempty": "Empty state", "richtext": "Rich text"}

// kindTitle is how the outline calls a kind: the widget's own label, else the plain name, else its title-cased id.
func kindTitle(k string, info map[string]builder.KindInfo) string {
	if l := info[k].Label; l != "" {
		return l
	}
	if n, ok := builderKindNames[k]; ok {
		return n
	}
	return schemaform.Humanize(k)
}

// outlineState is what one pass over the posted form learned.
type outlineState struct {
	nodeErrs map[string][]string
	formErrs map[string]schemaform.Errors // component field errors by node id
	raw      map[string]string            // typed values that did not coerce, by id+"\x00"+key
	summary  []ui.FieldError
}

func newOutlineState() *outlineState {
	return &outlineState{nodeErrs: map[string][]string{}, formErrs: map[string]schemaform.Errors{}, raw: map[string]string{}}
}

func (s *outlineState) fail(id, field, label, msg string) {
	s.nodeErrs[id] = append(s.nodeErrs[id], msg)
	s.summary = append(s.summary, ui.FieldError{ID: field, Label: label, Message: msg})
}

func propFieldID(id, key string) string { return "bo-" + id + "-p-" + key }

// Bind control names: the source select and the attribute path beside it.
func bindFieldName(id, key string) string { return "n." + id + ".b." + key }
func bindPathName(id, key string) string  { return "n." + id + ".bp." + key }

// bindValue splits a stored source into the option value of its select and
// the path typed after it: "entity.attributes.sku" is ("entity.attributes.", "sku").
func bindValue(src string) (opt, path string) {
	for _, s := range builder.BindSources() {
		if pre, ok := strings.CutSuffix(s.Source, "<path>"); ok && strings.HasPrefix(src, pre) {
			return pre, strings.TrimPrefix(src, pre)
		}
		if pre, ok := strings.CutSuffix(s.Source, "<key>"); ok && strings.HasPrefix(src, pre) {
			return pre, strings.TrimPrefix(src, pre)
		}
	}
	return src, ""
}

// walk visits nodes in document order.
func walk(ns []*builder.Node, f func(n, parent *builder.Node)) {
	var rec func(ns []*builder.Node, parent *builder.Node)
	rec = func(ns []*builder.Node, parent *builder.Node) {
		for _, n := range ns {
			f(n, parent)
			rec(n.C, n)
		}
	}
	rec(ns, nil)
}

func formHasPrefix(form url.Values, prefix string) bool {
	for k := range form {
		if strings.Contains(k, prefix) {
			return true
		}
	}
	return false
}

func lastValue(form url.Values, name string) (string, bool) {
	v, ok := form[name]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

// setNodeProp applies one setProp through Doc.Apply. A nil value deletes.
func setNodeProp(doc *builder.Doc, id, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = doc.Apply(builder.Op{Op: "setProp", ID: id, Key: key, Value: raw})
	return err
}

func sameValue(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// applyOutlineValues applies the typed values of every node in doc. Only nodes
// of doc are read: a posted name for any other id is ignored.
func (a *Admin) applyOutlineValues(doc *builder.Doc, form url.Values) *outlineState {
	st := newOutlineState()
	byType, _ := a.themeTypes()
	info := map[string]builder.KindInfo{}
	for _, k := range builder.Kinds() {
		info[k.Kind] = k
	}
	var nodes []*builder.Node
	walk(doc.Nodes, func(n, _ *builder.Node) { nodes = append(nodes, n) })
	for _, n := range nodes {
		label := nodeLabel(n, byType, info)
		for _, p := range info[n.T].Props {
			if outlineSkipProp[p.Key] || p.Type == "object" {
				continue
			}
			if p.Bind != "" && a.applyOutlineBind(doc, n, p, form, st, label) {
				continue // bound: the literal is unset and its input is ignored
			}
			raw, posted := lastValue(form, "n."+n.ID+".p."+p.Key)
			if !posted {
				continue
			}
			v, err := builder.CoerceProp(n.T, p.Key, raw)
			if err == nil && v == false {
				v = nil // an unticked box is no prop, as in the inspector
			}
			field := propFieldID(n.ID, p.Key)
			if err != nil {
				st.raw[n.ID+"\x00"+p.Key] = raw
				st.fail(n.ID, field, label+": "+schemaform.Humanize(p.Key), sentence(strings.TrimPrefix(err.Error(), "builder: ")))
				continue
			}
			if sameValue(n.P[p.Key], v) {
				continue
			}
			if err := setNodeProp(doc, n.ID, p.Key, v); err != nil {
				st.raw[n.ID+"\x00"+p.Key] = raw
				st.fail(n.ID, field, label+": "+schemaform.Humanize(p.Key), sentence(strings.TrimPrefix(err.Error(), "builder: ")))
			}
		}
		if n.T != "component" {
			continue
		}
		typ, _ := n.P["type"].(string)
		def, known := byType[typ]
		prefix := componentPrefix(n.ID)
		if !known || !formHasPrefix(form, prefix) {
			continue
		}
		prev, _ := n.P["content"].(map[string]any)
		content, errs, _ := a.decodeComponent(def, form, prefix, prev)
		if len(errs) > 0 {
			st.formErrs[n.ID] = errs
			for path, msgs := range errs {
				st.fail(n.ID, strings.ReplaceAll(prefix+path, ".", "-"), label+": "+schemaform.Humanize(path), msgs[0])
			}
		}
		if !sameValue(prev, content) {
			if err := setNodeProp(doc, n.ID, "content", content); err != nil {
				st.fail(n.ID, "bo-"+n.ID, label, sentence(strings.TrimPrefix(err.Error(), "builder: ")))
			}
		}
	}
	return st
}

// applyOutlineBind applies the posted source of a bindable prop as setBind
// (and unsets the literal). It reports whether the prop is bound afterwards or
// the bind was refused, so the caller leaves the literal input alone.
func (a *Admin) applyOutlineBind(doc *builder.Doc, n *builder.Node, p builder.PropInfo, form url.Values, st *outlineState, label string) (handled bool) {
	sel, posted := lastValue(form, bindFieldName(n.ID, p.Key))
	if !posted {
		return n.B[p.Key] != ""
	}
	src := strings.TrimSpace(sel)
	path, _ := lastValue(form, bindPathName(n.ID, p.Key))
	if strings.HasSuffix(src, ".") {
		src += strings.TrimSpace(path)
	}
	if src == n.B[p.Key] {
		return src != ""
	}
	var val json.RawMessage = json.RawMessage("null")
	if src != "" {
		val, _ = json.Marshal(src)
	}
	if _, err := doc.Apply(builder.Op{Op: "setBind", ID: n.ID, Key: p.Key, Value: val}); err != nil {
		st.raw[n.ID+"\x00b."+p.Key] = sel
		st.raw[n.ID+"\x00bp."+p.Key] = path
		st.fail(n.ID, bindFieldID(n.ID, p.Key), label+": "+schemaform.Humanize(p.Key)+" source", sentence(strings.TrimPrefix(err.Error(), "builder: ")))
		return true
	}
	if src == "" {
		return false
	}
	if _, has := n.P[p.Key]; has {
		if err := setNodeProp(doc, n.ID, p.Key, nil); err != nil {
			st.fail(n.ID, propFieldID(n.ID, p.Key), label+": "+schemaform.Humanize(p.Key), sentence(strings.TrimPrefix(err.Error(), "builder: ")))
		}
	}
	return true
}

func bindFieldID(id, key string) string { return "bo-" + id + "-b-" + key }

// nodeLabel is a short name for a node: its kind and the text it shows.
func nodeLabel(n *builder.Node, byType map[string]ThemeSection, info map[string]builder.KindInfo) string {
	title := kindTitle(n.T, info)
	short := ""
	switch n.T {
	case "component":
		typ, _ := n.P["type"].(string)
		title = "Section"
		if def, ok := byType[typ]; ok && def.Title != "" {
			title = def.Title
		} else if typ != "" {
			title = kindTitle(typ, info)
		}
		short, _ = n.P["title"].(string)
	default:
		short, _ = n.P["text"].(string)
		if short == "" {
			short, _ = n.P["alt"].(string)
		}
	}
	if r := []rune(short); len(r) > 40 {
		short = string(r[:40]) + "…"
	}
	if short != "" {
		return title + " “" + short + "”"
	}
	return title
}

// buildOutline turns doc into the form's node tree.
func (a *Admin) buildOutline(doc *builder.Doc, st *outlineState) []*outlineNode {
	byType, _ := a.themeTypes()
	info := map[string]builder.KindInfo{}
	for _, k := range builder.Kinds() {
		info[k.Kind] = k
	}
	var rec func(ns []*builder.Node, inLoop bool) []*outlineNode
	rec = func(ns []*builder.Node, inLoop bool) []*outlineNode {
		out := make([]*outlineNode, 0, len(ns))
		for i, n := range ns {
			on := &outlineNode{ID: n.ID, Kind: n.T, Legend: nodeLabel(n, byType, info), Orphan: isOrphan(info, n.T), CanNest: info[n.T].Children, First: i == 0, Last: i == len(ns)-1, Errors: st.nodeErrs[n.ID]}
			label := on.Legend
			for _, p := range info[n.T].Props {
				if outlineSkipProp[p.Key] || p.Type == "object" {
					continue
				}
				op := outlineProperty(n, p, label, st)
				if p.Bind != "" {
					op.Bind = outlineBindControl(n, p, inLoop, st)
				}
				on.Props = append(on.Props, op)
			}
			if n.T == "component" {
				typ, _ := n.P["type"].(string)
				if def, ok := byType[typ]; ok {
					content, _ := n.P["content"].(map[string]any)
					if html, err := a.renderComponentForm(def, content, componentPrefix(n.ID), st.formErrs[n.ID]); err == nil {
						on.Form = html
					}
				} else {
					on.Unknown = true
				}
			}
			on.Children = rec(n.C, inLoop || n.T == "loop")
			out = append(out, on)
		}
		return out
	}
	return rec(doc.Nodes, false)
}

// isOrphan reports whether kind is the shape of a plugin widget that is not
// registered: its node stays in the doc but has nothing to edit.
func isOrphan(info map[string]builder.KindInfo, kind string) bool {
	_, ok := info[kind]
	return !ok && builder.IsPackKind(kind)
}

// outlineBindControl builds the "bind to" select of prop p of n: the literal
// choice first, then the sources that fit the prop (loop item sources only
// inside a loop). The current source stays selectable even when it no longer
// fits, so a stored binding is never silently dropped by a save.
func outlineBindControl(n *builder.Node, p builder.PropInfo, inLoop bool, st *outlineState) *outlineBind {
	cur := n.B[p.Key]
	opt, path := bindValue(cur)
	if raw, bad := st.raw[n.ID+"\x00b."+p.Key]; bad {
		opt, path = raw, st.raw[n.ID+"\x00bp."+p.Key]
	}
	b := &outlineBind{
		ID: bindFieldID(n.ID, p.Key), Name: bindFieldName(n.ID, p.Key), Label: schemaform.Humanize(p.Key) + " source",
		PathID: bindFieldID(n.ID, p.Key) + "-path", PathName: bindPathName(n.ID, p.Key),
		Options: []outlineOption{{Value: "", Label: "Literal value", Selected: opt == ""}},
	}
	found := opt == ""
	for _, s := range builder.SourcesFor(p.Bind, inLoop) {
		v := strings.TrimSuffix(strings.TrimSuffix(s.Source, "<path>"), "<key>")
		label := s.Group + ": " + s.Label
		if v != s.Source {
			label += " (type the path)"
		}
		b.Options = append(b.Options, outlineOption{Value: v, Label: label, Selected: v == opt})
		found = found || v == opt
	}
	if !found {
		b.Options = append(b.Options, outlineOption{Value: opt, Label: opt, Selected: true})
	}
	b.ShowPath = strings.HasSuffix(opt, ".")
	b.PathValue = path
	return b
}

func outlineProperty(n *builder.Node, p builder.PropInfo, nodeLabel string, st *outlineState) outlineProp {
	op := outlineProp{ID: propFieldID(n.ID, p.Key), Name: "n." + n.ID + ".p." + p.Key, Label: schemaform.Humanize(p.Key), Control: "input", Type: "text"}
	cur, has := n.P[p.Key]
	raw, bad := st.raw[n.ID+"\x00"+p.Key]
	switch {
	case len(p.Enum) > 0:
		op.Control = "select"
		if !p.Required {
			op.Options = append(op.Options, outlineOption{Value: "", Label: "Default", Selected: !has})
		}
		for _, e := range p.Enum {
			s := fmt.Sprint(e)
			op.Options = append(op.Options, outlineOption{Value: s, Label: s, Selected: has && fmt.Sprint(cur) == s})
		}
	case p.Type == "boolean":
		op.Control = "checkbox"
		op.Checked, _ = cur.(bool)
		if bad {
			op.Checked = raw == "on"
		}
	case p.Type == "integer":
		op.Type = "number"
		op.Attrs = map[string]string{}
		if p.Min != nil {
			op.Attrs["min"] = strconv.FormatFloat(*p.Min, 'f', -1, 64)
		}
		if p.Max != nil {
			op.Attrs["max"] = strconv.FormatFloat(*p.Max, 'f', -1, 64)
		}
	default:
		switch p.Widget {
		case "textarea", "lines", "code", "richtext":
			op.Control = "textarea"
		default:
			if n.T == "text" && p.Key == "text" {
				op.Control = "textarea"
			}
			if (n.T == "text" || n.T == "heading") && p.Key == "html" {
				op.Control = "textarea"
				op.Help = "Inline markup (bold, italic, links). Overrides Text when set."
			}
		}
	}
	if op.Control != "select" && op.Control != "checkbox" {
		switch v := cur.(type) {
		case string:
			op.Value = v
		case float64:
			op.Value = strconv.FormatFloat(v, 'f', -1, 64)
		}
		if bad {
			op.Value = raw
		}
	}
	if bad && len(st.nodeErrs[n.ID]) > 0 {
		op.Error = st.nodeErrs[n.ID][len(st.nodeErrs[n.ID])-1]
	}
	return op
}

// outlineData assembles the template data for e with the working doc.
func (a *Admin) outlineData(e *entity.Entity, doc *builder.Doc, st *outlineState, plain bool, name string) (*outlineData, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return &outlineData{
		ID: e.ID.String(), Name: name, Status: string(e.Status), Version: strconv.Itoa(e.Version), Doc: string(raw),
		Nodes: a.buildOutline(doc, st), Kinds: a.outlineKinds(), Plain: plain,
		Published: e.Status == entity.StatusPublished, CanPublish: e.Status == entity.StatusDraft && !(e.Schema != nil && entity.IsReuseSchema(e.Schema)),
		DetailsHref: a.adminPath + "/entities/" + e.ID.String(),
		Summary:     st.summary,
	}, nil
}

func (a *Admin) renderOutline(w http.ResponseWriter, r *http.Request, status int, d *outlineData) {
	v := a.NewView(w, r, "Edit "+d.Name)
	v.Data = d
	v.Width = "full"
	v.SkipMarkupHooks = d.Shell != nil
	a.Render(w, r, status, "entities/builder", v)
}

// handleBuilderPost is the no-JS outline form handler.
func (a *Admin) handleBuilderPost(w http.ResponseWriter, r *http.Request, e *entity.Entity, _ *entity.EntitySchema, stored *builder.Doc) {
	r.Body = http.MaxBytesReader(w, r.Body, previewMaxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.Error(w, r, http.StatusRequestEntityTooLarge, "Too large", "This page is too large to save from the outline form.")
			return
		}
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	// The working doc is the one the form carries (earlier structural steps are
	// not saved yet); a form without a readable one starts from the stored doc.
	doc := stored
	if raw := r.PostFormValue("doc"); raw != "" {
		if d, err := builder.Parse([]byte(raw)); err == nil {
			doc = d
		}
	}
	// A missing or unreadable version is stale, as in the classic editor.
	posted, _ := strconv.Atoi(r.PostFormValue("version"))
	stale := posted != e.Version
	op := r.PostFormValue("_op")
	verb, arg, _ := strings.Cut(op, ".")
	plain := true

	title := e.Name
	if _, ok := r.PostForm["name"]; ok {
		title = r.PostFormValue("name")
	}
	st := a.applyOutlineValues(doc, r.PostForm)
	byType, _ := a.themeTypes()

	notice := ""
	opFailed := false
	fail := func(msg string) {
		opFailed = true
		st.summary = append(st.summary, ui.FieldError{ID: "bo-name", Label: "Page", Message: msg})
	}
	switch verb {
	case "up", "down":
		n, parent := doc.Find(arg)
		if n == nil {
			break
		}
		sibs := doc.Nodes
		pid := ""
		if parent != nil {
			sibs, pid = parent.C, parent.ID
		}
		i := 0
		for j, s := range sibs {
			if s.ID == arg {
				i = j
			}
		}
		to := i - 1
		if verb == "down" {
			to = i + 1
		}
		if to < 0 || to >= len(sibs) {
			break
		}
		if _, err := doc.Apply(builder.Op{Op: "move", ID: arg, Parent: pid, Index: to}); err != nil {
			fail(sentence(strings.TrimPrefix(err.Error(), "builder: ")))
		}
	case "del":
		if n, _ := doc.Find(arg); n != nil {
			if _, err := doc.Apply(builder.Op{Op: "remove", ID: arg}); err != nil {
				fail(sentence(strings.TrimPrefix(err.Error(), "builder: ")))
			} else {
				notice = "Removed from the draft. Save to keep the change."
			}
		}
	case "after", "into", "add":
		kind := r.PostFormValue("kind." + arg)
		if verb == "add" {
			kind = r.PostFormValue("add_kind")
		}
		node, problem := newOutlineNode(kind, byType)
		if problem != "" {
			notice = problem
			break
		}
		ins := builder.Op{Op: "insert", Node: node, Index: len(doc.Nodes)}
		switch verb {
		case "after":
			n, parent := doc.Find(arg)
			if n == nil {
				notice = "Choose where to add."
				break
			}
			sibs := doc.Nodes
			if parent != nil {
				sibs, ins.Parent = parent.C, parent.ID
			}
			for j, s := range sibs {
				if s.ID == arg {
					ins.Index = j + 1
				}
			}
		case "into":
			n, _ := doc.Find(arg)
			if n == nil || !outlineKindNests(n.T) {
				notice = "Choose where to add."
				break
			}
			ins.Parent, ins.Index = arg, len(n.C)
		}
		if notice != "" {
			break
		}
		if _, err := doc.Apply(ins); err != nil {
			fail(sentence(strings.TrimPrefix(err.Error(), "builder: ")))
		}
	}

	rerender := func(status int, conflict bool) {
		d, err := a.outlineData(e, doc, st, plain, title)
		if err != nil {
			a.serverError(w, r, "The page outline could not be built", err)
			return
		}
		// Every re-render keeps the version the draft is based on, so a later
		// save is still checked against it; only the conflict page shows the
		// current one: saving from it is the explicit overwrite.
		d.Version = strconv.Itoa(posted)
		if conflict {
			d.Version = strconv.Itoa(e.Version)
			d.Conflict = staleMessage(posted, e.Version)
		}
		d.Notice = notice
		d.Outline = r.PostFormValue("outline") == "1"
		a.renderOutline(w, r, status, d)
	}

	structural := verb != "save" && verb != "publish" && op != "" || schemaform.Structural(r.PostForm)
	if structural {
		// Typed values that did not take show beside their fields; only an
		// operation the doc refused (depth, size) is an error status.
		if opFailed {
			rerender(http.StatusUnprocessableEntity, false)
			return
		}
		rerender(http.StatusOK, false)
		return
	}
	if len([]rune(strings.TrimSpace(title))) > 255 {
		st.summary = append(st.summary, ui.FieldError{ID: "bo-name", Label: "Title", Message: "Use at most 255 characters"})
	}
	if verb == "publish" && untitledForPublish(strings.TrimSpace(title), e) {
		st.summary = append(st.summary, ui.FieldError{ID: "bo-name", Label: "Title", Message: "Give the page a title before publishing it"})
	}
	if len(st.summary) > 0 {
		rerender(http.StatusUnprocessableEntity, false)
		return
	}
	if stale {
		rerender(http.StatusConflict, true)
		return
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		a.serverError(w, r, "The page could not be saved", err)
		return
	}
	saveOp := opSave
	if verb == "publish" {
		saveOp = opPublish
	}
	saved, err := a.saveBuilderDoc(r.Context(), actorID(r), e, raw, saveOp, &title)
	if err != nil {
		var p *saveProblem
		if errors.As(err, &p) {
			if p.Status == http.StatusNotFound {
				a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
				return
			}
			st.summary = append(st.summary, ui.FieldError{ID: "bo-name", Label: "Page", Message: p.Msg})
			rerender(p.Status, false)
			return
		}
		a.serverError(w, r, "The page could not be saved", err)
		return
	}
	msg, kind := "Saved.", "success"
	if verb == "publish" && saved.Entity.Status == entity.StatusPublished {
		msg = "Saved and published."
	}
	if saved.Note != "" {
		msg, kind = strings.TrimSpace(msg+" "+saved.Note), "error"
		if verb == "publish" && saved.Entity.Status != entity.StatusPublished && !strings.HasPrefix(saved.Note, "Saved") {
			msg = "Saved. " + saved.Note
		}
	}
	a.SetFlash(w, r, kind, msg)
	target := a.adminPath + "/entities/" + e.ID.String() + "/edit"
	if r.PostFormValue("outline") == "1" {
		target += "?outline=1"
	}
	Redirect(w, r, target)
}

// outlineKindNests reports whether nodes of kind may hold children.
func outlineKindNests(kind string) bool {
	for _, k := range builder.Kinds() {
		if k.Kind == kind {
			return k.Children
		}
	}
	return false
}

// newOutlineNode makes the node an "add" names; the string is the problem to
// show when the choice is unusable.
func newOutlineNode(kind string, byType map[string]ThemeSection) (*builder.Node, string) {
	if kind == "" {
		return nil, "Choose the kind of element to add."
	}
	var n *builder.Node
	var err error
	if typ, ok := strings.CutPrefix(kind, "component:"); ok {
		if _, known := byType[typ]; !known {
			return nil, "Choose the kind of element to add."
		}
		n, err = builder.NewComponent(typ, newNodeID())
	} else {
		var t builder.Node
		if t, err = builder.NewTree(kind); err == nil { // seeded children for the structural kinds
			n = &t
		}
	}
	if err != nil {
		return nil, "Choose the kind of element to add."
	}
	return n, ""
}
