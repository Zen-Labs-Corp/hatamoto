package schemaform

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type outcome int

const (
	untouched outcome = iota // the form did not carry this field: keep the stored value
	set
	unset // the user emptied it: remove the key
)

type decoder struct {
	form      url.Values
	opt       Options
	errs      Errors
	keepEmpty bool // structural re-render: keep blank repeater items
}

func (d *decoder) errorf(path, msg string) {
	if d.errs == nil {
		d.errs = Errors{}
	}
	d.errs[path] = append(d.errs[path], msg)
}

// Decode reads the submitted form back into JSON-shaped values, driven by the
// schema (no type hints travel with the form). Only properties the schema lists
// are overlaid on prev: unknown keys (legacy WordPress fields, aliases) survive
// a save untouched, and editing a canonical property removes its x-hm-alias
// keys so the stale alias cannot shadow the new value. Inside nested objects
// and repeater items the unknown keys travel in the form itself (the hidden
// _keep.<path> inputs Render emits), so they survive reordering too.
//
// Add/move/remove buttons (_add./_move./_remove.) are applied here; check
// Structural(form) to re-render instead of saving.
func Decode(schema json.RawMessage, form url.Values, prev map[string]any, opt Options) (map[string]any, Errors) {
	root, err := ParseSchema(schema)
	if err != nil {
		return prev, Errors{"": {err.Error()}}
	}
	d := &decoder{form: form, opt: opt, keepEmpty: Structural(form)}
	out := cloneMap(prev)
	for _, p := range root.Props {
		val, oc := d.node(p.Name, p.Node, p.Name, p.Name, root.isRequired(p.Name), valueOf(prev, p.Name, p.Node))
		d.apply(out, p.Name, p.Node, val, oc)
	}
	return out, d.errs
}

func (d *decoder) apply(out map[string]any, name string, n *Node, val any, oc outcome) {
	switch oc {
	case set:
		out[name] = val
	case unset:
		delete(out, name)
	default:
		return
	}
	for _, a := range n.Alias {
		delete(out, a)
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (d *decoder) key(path string) string { return d.opt.Prefix + path }

func (d *decoder) has(path string) bool {
	_, ok := d.form[d.key(path)]
	return ok
}

func (d *decoder) present(path string) bool {
	_, ok := d.form["_present."+d.key(path)]
	return ok
}

// node decodes one property. readPath addresses the submitted fields, errPath
// the Errors key (they differ only while repeater indices are being compacted).
func (d *decoder) node(name string, n *Node, readPath, errPath string, required bool, pv any) (any, outcome) {
	hint := d.opt.UI[hintKey(readPath)]
	b := &builder{opt: d.opt}
	k := b.kind(name, n, pv, hint)
	if n.Type == "string" && len(n.Enum) == 0 && !d.has(readPath) && d.hasChildren(readPath) {
		// Repeater items have no stored value to inspect, but a string property
		// that holds an object was rendered as path.<key> inputs.
		k = "object-synth"
	}
	switch k {
	case "select":
		if !d.has(readPath) {
			return nil, untouched
		}
		v := strings.TrimSpace(d.form.Get(d.key(readPath)))
		if v == "" {
			return nil, unset
		}
		for _, e := range n.Enum {
			if e == v {
				return v, set
			}
		}
		d.errorf(errPath, "Choose one of the options")
		return nil, untouched
	case "text", "image", "textarea", "richtext", "html":
		if !d.has(readPath) {
			return nil, untouched
		}
		v := d.form.Get(d.key(readPath))
		if k == "text" || k == "image" {
			v = strings.TrimSpace(v)
		}
		// Textareas arrive with CRLF line breaks from browsers.
		v = strings.ReplaceAll(v, "\r\n", "\n")
		if v == "" {
			return nil, unset
		}
		return v, set
	case "number":
		if !d.has(readPath) {
			return nil, untouched
		}
		v := strings.TrimSpace(d.form.Get(d.key(readPath)))
		if v == "" {
			return nil, unset
		}
		if n.Type == "integer" {
			i, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				d.errorf(errPath, "Enter a whole number")
				return nil, untouched
			}
			return i, set
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			d.errorf(errPath, "Enter a number")
			return nil, untouched
		}
		return f, set
	case "checkbox":
		// An unchecked box posts nothing, so the hidden _present sentinel is
		// what says "this box was on the form and is off".
		state, ok := d.form["_present."+d.key(readPath)]
		if !ok {
			return nil, untouched
		}
		checked := d.form.Get(d.key(readPath)) == "true"
		if len(state) > 0 && state[0] == "default" {
			// The key was absent and the form showed the schema default: leaving
			// the box as shown must not write a value.
			if def, _ := n.Default.(bool); checked == def {
				return nil, untouched
			}
		}
		return checked, set
	case "tags", "lines", "entityref":
		if !d.present(readPath) {
			return nil, untouched
		}
		var list []string
		seen := map[string]bool{}
		for _, raw := range d.form[d.key(readPath)] {
			// "lines" keeps commas inside an item; tags also split on them.
			for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return (r == ',' && k != "lines") || r == '\n' || r == '\r' }) {
				part = strings.TrimSpace(part)
				if part == "" || seen[part] {
					continue
				}
				seen[part] = true
				list = append(list, part)
			}
		}
		if len(list) > MaxItems {
			d.errorf(errPath, fmt.Sprintf("Add at most %d items", MaxItems))
			return nil, untouched
		}
		if k == "entityref" {
			for _, id := range list {
				if _, err := uuid.Parse(id); err != nil {
					d.errorf(errPath, "Enter valid entity IDs")
					return nil, untouched
				}
			}
			if n.XMultiple != nil && !*n.XMultiple && len(list) > 1 {
				d.errorf(errPath, "Choose only one")
				return nil, untouched
			}
		}
		if len(list) == 0 {
			return nil, unset
		}
		out := make([]any, len(list))
		for i, s := range list {
			out[i] = s
		}
		return out, set
	case "object", "object-synth":
		return d.object(n, k, readPath, errPath, required, pv)
	case "repeater":
		return d.repeater(n, readPath, errPath)
	}
	return nil, untouched
}

// hasChildren reports submitted fields under path ("path.<key>").
func (d *decoder) hasChildren(path string) bool {
	prefix := d.key(path) + "."
	for key := range d.form {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// keep reads the _keep.<path> input: the keys Render could not offer as
// controls. Bad or oversized JSON is ignored (the keys are simply not kept).
func (d *decoder) keep(path string) map[string]any {
	raw := d.form.Get("_keep." + d.key(path))
	if raw == "" || len(raw) > maxKeepBytes {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}

// maxKeepBytes bounds one _keep input.
const maxKeepBytes = 256 << 10

func (d *decoder) object(n *Node, kind, readPath, errPath string, required bool, pv any) (any, outcome) {
	prev, _ := pv.(map[string]any)
	m := cloneMap(prev)
	for k, v := range d.keep(readPath) {
		m[k] = v
	}
	if kind == "object-synth" {
		// A string property that holds an object: its sub-keys are only known
		// from the submission (path.<key>).
		prefix := d.key(readPath) + "."
		touched := false
		for key, vals := range d.form {
			sub, ok := strings.CutPrefix(key, prefix)
			if !ok || sub == "" || strings.ContainsRune(sub, '.') || len(vals) == 0 {
				continue
			}
			touched = true
			if v := strings.TrimSpace(vals[0]); v != "" {
				m[sub] = v
			} else {
				delete(m, sub)
			}
		}
		if !touched {
			return nil, untouched
		}
		if len(m) == 0 && !required {
			return nil, unset
		}
		return m, set
	}
	touched := false
	for _, p := range n.Props {
		v, oc := d.node(p.Name, p.Node, readPath+"."+p.Name, errPath+"."+p.Name, n.isRequired(p.Name), valueOf(m, p.Name, p.Node))
		if oc != untouched {
			touched = true
		}
		d.apply(m, p.Name, p.Node, v, oc)
	}
	if !touched {
		return nil, untouched
	}
	if len(m) == 0 && !required {
		return nil, unset
	}
	return m, set
}

// indices lists the item indices present under path ("faqs.<i>." or "faqs.<i>").
func (d *decoder) indices(path string) ([]int, bool) {
	prefix := d.key(path) + "."
	seen := map[int]bool{}
	for k := range d.form {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		seg, _, _ := strings.Cut(rest, ".")
		i, err := strconv.Atoi(seg)
		if err != nil || i < 0 {
			continue
		}
		seen[i] = true
	}
	list := make([]int, 0, len(seen))
	for i := range seen {
		list = append(list, i)
	}
	sort.Ints(list)
	if len(list) > MaxItems || (len(list) > 0 && list[len(list)-1] >= MaxItems*100) {
		return nil, false
	}
	return list, true
}

func (d *decoder) repeater(n *Node, readPath, errPath string) (any, outcome) {
	if !d.present(readPath) {
		return nil, untouched
	}
	idx, ok := d.indices(readPath)
	if !ok {
		d.errorf(errPath, fmt.Sprintf("Add at most %d items", MaxItems))
		return nil, untouched
	}
	it := n.Items
	if it == nil {
		it = &Node{Type: "string"}
	}
	scalar := !(it.Type == "object" && len(it.Props) > 0)

	// Structural buttons act on the submitted order before decoding.
	order := append([]int(nil), idx...)
	name := d.key(readPath)
	if v := d.form.Get("_remove." + name); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			order = removeValue(order, i)
		}
	}
	if v := d.form.Get("_move." + name); v != "" {
		idxStr, dir, _ := strings.Cut(v, ":")
		if i, err := strconv.Atoi(idxStr); err == nil {
			order = move(order, i, dir)
		}
	}
	added := d.form.Get("_add."+name) != ""

	var items []any
	for _, i := range order {
		rp := readPath + "." + strconv.Itoa(i)
		ep := errPath + "." + strconv.Itoa(len(items))
		var val any
		var blank bool
		if scalar {
			v, oc := d.node(lastSeg(readPath), it, rp, ep, false, nil)
			blank = oc != set
			val = v
		} else {
			// The item starts from the keys the form could not edit (_keep), then
			// the schema's properties are laid over it.
			m := cloneMap(d.keep(rp))
			sets := 0
			for _, p := range it.Props {
				v, oc := d.node(p.Name, p.Node, rp+"."+p.Name, ep+"."+p.Name, it.isRequired(p.Name), valueOf(m, p.Name, p.Node))
				if oc == set {
					sets++
				}
				d.apply(m, p.Name, p.Node, v, oc)
			}
			blank = sets == 0
			val = m
		}
		if blank && !d.keepEmpty {
			continue
		}
		items = append(items, val)
	}
	if added {
		if len(items) >= MaxItems {
			d.errorf(errPath, fmt.Sprintf("Add at most %d items", MaxItems))
		} else if scalar {
			items = append(items, "")
		} else {
			items = append(items, defaultItem(it))
		}
	}
	if len(items) == 0 {
		return nil, unset
	}
	return items, set
}

func removeValue(order []int, v int) []int {
	out := order[:0:0]
	for _, x := range order {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// move swaps the item whose submitted index is v with its neighbour.
func move(order []int, v int, dir string) []int {
	pos := -1
	for p, x := range order {
		if x == v {
			pos = p
		}
	}
	if pos < 0 {
		return order
	}
	to := pos - 1
	if dir == "down" {
		to = pos + 1
	}
	if to < 0 || to >= len(order) {
		return order
	}
	out := append([]int(nil), order...)
	out[pos], out[to] = out[to], out[pos]
	return out
}

// defaultItem is the new item the add button appends: the schema's defaults,
// like the Next admin's createDefaultItem (other properties stay absent).
func defaultItem(it *Node) map[string]any {
	m := map[string]any{}
	for _, p := range it.Props {
		if p.Node.Default != nil {
			m[p.Name] = p.Node.Default
		}
	}
	return m
}
