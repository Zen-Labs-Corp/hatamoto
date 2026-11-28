package wpimport

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// acfKind says how the values of an ACF field are read and written.
type acfKind string

const (
	acfKindNone     acfKind = ""         // layout only: message, tab, accordion
	acfKindText     acfKind = "text"     // plain string
	acfKindRichtext acfKind = "richtext" // HTML string
	acfKindNumber   acfKind = "number"
	acfKindBool     acfKind = "bool"
	acfKindChoice   acfKind = "choice" // one value of an enum
	acfKindMulti    acfKind = "multi"  // a list of strings
	acfKindDate     acfKind = "date"
	acfKindDateTime acfKind = "datetime"
	acfKindTime     acfKind = "time"
	acfKindLink     acfKind = "link" // the url of a link array
	acfKindMedia    acfKind = "media"
	acfKindGallery  acfKind = "gallery"
	acfKindRelation acfKind = "relation"
	acfKindRepeater acfKind = "repeater"
	acfKindGroup    acfKind = "group"
	acfKindFlex     acfKind = "flex"
	acfKindRowText  acfKind = "rowtext" // nested repeater or flexible content, kept as text
)

// acfSchemaBudget is how large a schema may grow before more fields are left
// out; the hard limit of the schema validator is 64 KiB.
const acfSchemaBudget = 60 * 1024

// acfReservedProps are core properties of the built-in types; an ACF field
// with such a name gets the prefix "acf_".
var acfReservedProps = map[string]bool{"content": true, "excerpt": true, "featured_image": true, "meta_title": true,
	"meta_description": true, "og_image": true, "no_index": true, "seo": true, "tags": true, "author": true, "title": true, "slug": true}

// acfUnsafeProps are names the schema validator refuses.
var acfUnsafeProps = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}

// acfMapped is one ACF field aimed at a property of a post type's schema.
type acfMapped struct {
	Prop  string
	Field ACFField
	Group *ACFGroup
	Kind  acfKind
	Node  map[string]any
	// Builtin is true for post and page: their schema JSON is never written, so
	// the values are stored as attributes without a property.
	Builtin bool
	// Skipped is true when no value may be written to Prop: the property exists
	// with another type, or the schema had no room for it.
	Skipped bool
	Lossy   bool
	// Stored is the property the schema already had, when the values follow it.
	Stored map[string]any
}

// propNode is a property to add to a schema.
type propNode struct {
	Name string
	Node map[string]any
}

// sanitizePropName turns an ACF field name into a property name.
func sanitizePropName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	n := b.String()
	if n == "" {
		n = "field"
	}
	if c := n[0]; c >= '0' && c <= '9' {
		n = "f_" + n
	}
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// dedupeProp returns name, or name_2, name_3 and so on when taken, and marks it taken.
func dedupeProp(name string, taken map[string]bool) string {
	n := name
	for i := 2; taken[n]; i++ {
		n = fmt.Sprintf("%s_%d", name, i)
	}
	taken[n] = true
	return n
}

// propName is the top-level property name of an ACF field: lowercase, safe
// characters, the prefix "acf_" for core keys, and a numeric suffix when the
// name is already taken.
func propName(name string, taken map[string]bool) string {
	n := sanitizePropName(name)
	if acfReservedProps[n] || acfUnsafeProps[n] {
		n = "acf_" + n
	}
	return dedupeProp(n, taken)
}

// rowName is the property name of a sub field inside a row object. Core keys
// are fine there; only unsafe names are changed.
func rowName(name string, taken map[string]bool) string {
	n := sanitizePropName(name)
	if acfUnsafeProps[n] {
		n = "acf_" + n
	}
	return dedupeProp(n, taken)
}

// subNames returns the row property name of each sub field, in order.
func subNames(subs []ACFField) []string {
	taken := map[string]bool{}
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = rowName(s.Name, taken)
	}
	return out
}

// layoutNames returns, per layout and sub field, the property name inside the
// shared row object of a flexible content field. A name used by two layouts
// with different shapes becomes "<layout>_<name>" for the second.
func layoutNames(layouts []ACFLayout) [][]string {
	shapes := map[string]map[string]any{"layout": nil}
	out := make([][]string, len(layouts))
	for li, l := range layouts {
		out[li] = make([]string, len(l.Sub))
		inLayout := map[string]bool{}
		for si, s := range l.Sub {
			base := sanitizePropName(s.Name)
			if acfUnsafeProps[base] {
				base = "acf_" + base
			}
			node, _, _, _ := acfNode(s, true)
			fits := func(c string) bool {
				prev, used := shapes[c]
				return !used || (prev != nil && sameShape(prev, node) && !inLayout[c])
			}
			n := base
			if !fits(n) {
				n = sanitizePropName(layoutLabel(l) + "_" + s.Name)
				for i := 2; !fits(n); i++ {
					n = fmt.Sprintf("%s_%d", sanitizePropName(layoutLabel(l)+"_"+s.Name), i)
				}
			}
			shapes[n] = node
			inLayout[n] = true
			out[li][si] = n
		}
	}
	return out
}

func layoutLabel(l ACFLayout) string { return firstNonEmpty(l.Name, l.Key) }

// orderedObject writes a JSON object with its members in the given order.
func orderedObject(names []string, vals map[string]any) (jsontext.Value, error) {
	b := []byte{'{'}
	for i, n := range names {
		if i > 0 {
			b = append(b, ',')
		}
		k, err := jsonx.Marshal(n)
		if err != nil {
			return nil, err
		}
		v, err := jsonx.Marshal(vals[n])
		if err != nil {
			return nil, err
		}
		b = append(append(append(b, k...), ':'), v...)
	}
	return append(b, '}'), nil
}

func textNode(f ACFField, extra map[string]any) map[string]any {
	n := map[string]any{"type": "string", "title": f.Label}
	for k, v := range extra {
		n[k] = v
	}
	return n
}

// acfNode is the schema property of an ACF field (decision D3 of the plan).
// inRow is true inside a repeater, group or flexible content row, where a
// nested repeater or flexible content becomes text. lossy means something of
// the field is not carried over; warn names a type that was stored as text
// for lack of a better mapping. A layout-only field has a nil node.
func acfNode(f ACFField, inRow bool) (node map[string]any, kind acfKind, lossy bool, warn string) {
	switch f.Type {
	case "text", "password", "color_picker":
		return textNode(f, nil), acfKindText, false, ""
	case "textarea":
		return textNode(f, map[string]any{"maxLength": 65535}), acfKindText, false, ""
	case "wysiwyg":
		return textNode(f, map[string]any{"x-hm-field": "richtext"}), acfKindRichtext, false, ""
	case "email":
		return textNode(f, map[string]any{"format": "email"}), acfKindText, false, ""
	case "url", "oembed":
		return textNode(f, map[string]any{"format": "uri"}), acfKindText, false, ""
	case "link":
		return textNode(f, map[string]any{"format": "uri"}), acfKindLink, false, ""
	case "number", "range":
		return map[string]any{"type": "number", "title": f.Label}, acfKindNumber, false, ""
	case "true_false":
		return map[string]any{"type": "boolean", "title": f.Label}, acfKindBool, false, ""
	case "select", "radio", "button_group", "checkbox":
		if f.Type == "checkbox" || f.Multiple {
			return map[string]any{"type": "array", "title": f.Label, "items": map[string]any{"type": "string"}}, acfKindMulti, false, ""
		}
		n := textNode(f, nil)
		var enum []string
		for _, c := range f.Choices {
			if c.Value != "" && !hasString(enum, c.Value) {
				enum = append(enum, c.Value)
			}
		}
		if len(enum) > 0 {
			n["enum"] = enum
		}
		return n, acfKindChoice, false, ""
	case "date_picker":
		return textNode(f, map[string]any{"format": "date"}), acfKindDate, false, ""
	case "date_time_picker":
		return textNode(f, map[string]any{"format": "date-time"}), acfKindDateTime, false, ""
	case "time_picker":
		return textNode(f, nil), acfKindTime, false, ""
	case "image", "file":
		return textNode(f, map[string]any{"format": "uri", "x-media": true}), acfKindMedia, false, ""
	case "gallery":
		return map[string]any{"type": "array", "title": f.Label,
			"items": map[string]any{"type": "string", "format": "uri", "x-media": true}}, acfKindGallery, false, ""
	case "relationship", "post_object", "page_link":
		multi := f.Type == "relationship" || f.Multiple
		return map[string]any{"type": "array", "title": f.Label, "items": map[string]any{"type": "string"}, "x-multiple": multi}, acfKindRelation, false, ""
	case "message", "tab", "accordion":
		return nil, acfKindNone, false, ""
	case "repeater", "group", "flexible_content":
		if inRow {
			return textNode(f, map[string]any{"maxLength": 65535}), acfKindRowText, true, ""
		}
		return containerNode(f)
	}
	return textNode(f, nil), acfKindText, false, fmt.Sprintf("ACF field type %q stored as text", f.Type)
}

// containerNode builds the property of a repeater, group or flexible content field.
func containerNode(f ACFField) (map[string]any, acfKind, bool, string) {
	props := map[string]any{}
	var order []string
	lossy := false
	add := func(name string, s ACFField) {
		n, k, l, _ := acfNode(s, true)
		if n == nil || (props[name] != nil) {
			return
		}
		props[name] = n
		order = append(order, name)
		lossy = lossy || l || k == acfKindRowText
	}
	kind := acfKindRepeater
	switch f.Type {
	case "group":
		kind = acfKindGroup
		names := subNames(f.Sub)
		for i, s := range f.Sub {
			add(names[i], s)
		}
	case "flexible_content":
		kind = acfKindFlex
		var enum []string
		for _, l := range f.Layouts {
			enum = append(enum, layoutLabel(l))
		}
		layout := map[string]any{"type": "string", "title": "Layout"}
		if len(enum) > 0 {
			layout["enum"] = enum
		}
		props["layout"] = layout
		order = append(order, "layout")
		names := layoutNames(f.Layouts)
		for li, l := range f.Layouts {
			for si, s := range l.Sub {
				add(names[li][si], s)
			}
		}
	default:
		names := subNames(f.Sub)
		for i, s := range f.Sub {
			add(names[i], s)
		}
	}
	raw, err := orderedObject(order, props)
	if err != nil {
		return textNode(f, map[string]any{"maxLength": 65535}), acfKindRowText, true, ""
	}
	obj := map[string]any{"type": "object", "properties": raw}
	if kind == acfKindGroup {
		obj["title"] = f.Label
		return obj, kind, lossy, ""
	}
	return map[string]any{"type": "array", "title": f.Label, "items": obj, "x-hm-item-label": f.Label}, kind, lossy, ""
}

// shapeKeys are decorations that may differ without changing what a property stores.
var decorationKeys = map[string]bool{"title": true, "description": true, "x-hm-group": true, "x-hm-item-label": true,
	"x-hm-alias": true, "x-entity-schema": true, "x-multiple": true, "enum": true, "maxLength": true}

// normalise round-trips v through JSON and drops decorations, so a node built
// here compares equal to the same node read back from a stored schema.
func normalise(v any) any {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := jsonx.Unmarshal(raw, &out, jsonx.StoredLoose); err != nil {
		return v
	}
	return strip(out)
}

func strip(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if !decorationKeys[k] {
				out[k] = strip(x)
			}
		}
		return out
	case []any:
		for i := range t {
			t[i] = strip(t[i])
		}
	}
	return v
}

// sameShape reports whether two property nodes store the same kind of value:
// same type, format, items, media flag and editor kind.
func sameShape(a, b map[string]any) bool {
	return reflect.DeepEqual(normalise(a), normalise(b))
}

// mergeProps adds properties to a schema and never touches the ones it has.
// A key that exists with the same shape is reported in existing; one that
// exists with another shape is a clash and stays as it is. Properties keep
// their order and the new ones follow.
func mergeProps(schema jsontext.Value, add []propNode) (merged jsontext.Value, added, existing, clashes []string, err error) {
	root, err := jsonx.ObjectEntries(schema)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read schema: %w", err)
	}
	at := -1
	for i, e := range root {
		if e.Key == "properties" {
			at = i
		}
	}
	if at < 0 {
		return nil, nil, nil, nil, fmt.Errorf("read schema: no properties")
	}
	props, err := jsonx.ObjectEntries(root[at].Value)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read schema properties: %w", err)
	}
	have := map[string]jsontext.Value{}
	for _, p := range props {
		have[p.Key] = p.Value
	}
	b := []byte{'{'}
	write := func(key string, val []byte) {
		k, _ := jsonx.Marshal(key)
		if len(b) > 1 {
			b = append(b, ',')
		}
		b = append(append(append(b, k...), ':'), val...)
	}
	var newProps []propNode
	for _, p := range add {
		cur, ok := have[p.Name]
		if !ok {
			newProps = append(newProps, p)
			added = append(added, p.Name)
			have[p.Name] = nil
			continue
		}
		var old map[string]any
		if cur != nil {
			_ = jsonx.Unmarshal(cur, &old, jsonx.StoredLoose)
		}
		if old != nil && sameShape(old, p.Node) {
			existing = append(existing, p.Name)
		} else {
			clashes = append(clashes, p.Name)
		}
	}
	for i, e := range root {
		if i != at {
			write(e.Key, e.Value)
			continue
		}
		ob := []byte{'{'}
		put := func(key string, val []byte) {
			k, _ := jsonx.Marshal(key)
			if len(ob) > 1 {
				ob = append(ob, ',')
			}
			ob = append(append(append(ob, k...), ':'), val...)
		}
		for _, p := range props {
			put(p.Key, p.Value)
		}
		for _, p := range newProps {
			v, err := jsonx.Marshal(p.Node)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("write property %s: %w", p.Name, err)
			}
			put(p.Name, v)
		}
		write(e.Key, append(ob, '}'))
	}
	return jsontext.Value(append(b, '}')), added, existing, clashes, nil
}

// acfSourceID is the id-map key of a field on one schema. A group shown on
// several types maps each type on its own, so a failed type is retried alone.
func acfSourceID(g *ACFGroup, f ACFField, slug string) string {
	key := f.Key
	switch {
	case g.Guessed:
		key = "guess:" + g.Types[0] + ":" + f.Name
	case key == "":
		key = "name:" + g.ID + ":" + f.Name
	}
	if len(g.Types) > 1 {
		key += "@" + slug
	}
	return key
}

// schemasStep prepares the schemas and then adds the ACF fields to them.
func (r *Runner) schemasStep(ctx context.Context) error {
	if err := r.prepareSchemas(ctx); err != nil {
		return err
	}
	return r.importACFSchema(ctx)
}

// importACFSchema aims each ACF field group at the schemas of its post types:
// missing properties are added, existing ones are never removed or retyped.
// Post and page are built-in, so their schema JSON is left alone and their
// values are kept as attributes. A failure adds a warning and a report line;
// it never fails the run.
func (r *Runner) importACFSchema(ctx context.Context) error {
	if len(r.dump.ACF) == 0 {
		return nil
	}
	res := &ACFResult{Groups: len(r.dump.ACF)}
	r.res.ACF = res
	r.acf = map[string][]acfMapped{}
	for _, g := range r.dump.ACF {
		res.TypesGuessed = res.TypesGuessed || g.Guessed
	}
	done, err := r.st.All(ctx, KindACFField)
	if err != nil {
		return fmt.Errorf("read the imported ACF fields: %w", err)
	}
	assigned := map[string]map[string]string{} // type -> ACF name -> property
	taken := map[string]map[string]bool{}      // type -> property names in use
	warned := map[string]bool{}
	warn := func(format string, a ...any) {
		if m := fmt.Sprintf(format, a...); !warned[m] {
			warned[m] = true
			r.res.Warnings = append(r.res.Warnings, m)
		}
	}
	line := func(g *ACFGroup, f ACFField, slug, status, note string) {
		res.Fields = append(res.Fields, ACFFieldResult{Group: g.Title, Label: firstNonEmpty(f.Label, f.Name), Type: f.Type, Target: slug, Status: status, Note: note})
	}
	for gi := range r.dump.ACF {
		g := &r.dump.ACF[gi]
		for _, loc := range g.OtherLocations {
			res.Fields = append(res.Fields, ACFFieldResult{Group: g.Title, Label: loc, Type: "location", Status: "skipped", Note: "location not imported: " + loc})
		}
		for _, typ := range g.Types {
			slug := r.schemaOf[typ]
			if slug == "" {
				for _, f := range g.Fields {
					line(g, f, "", "skipped", "type not imported: "+typ)
				}
				continue
			}
			if assigned[typ] == nil {
				assigned[typ], taken[typ] = map[string]string{}, map[string]bool{}
			}
			r.mapGroup(ctx, g, typ, slug, assigned[typ], taken[typ], done, line, warn)
		}
	}
	return nil
}

// mapGroup maps one field group onto one schema.
func (r *Runner) mapGroup(ctx context.Context, g *ACFGroup, typ, slug string, assigned map[string]string, taken map[string]bool,
	done map[string]uuid.UUID, line func(*ACFGroup, ACFField, string, string, string), warn func(string, ...any)) {
	res := r.res.ACF
	sc, err := r.schema(ctx, slug)
	if err != nil {
		warn("ACF group %q not imported into %q: %v", g.Title, slug, err)
		for _, f := range g.Fields {
			line(g, f, slug, "skipped", "schema not available")
		}
		return
	}
	have := schemaProps(sc.Schema)
	var plans []*acfPlan
	for _, f := range g.Fields {
		node, kind, lossy, w := acfNode(f, false)
		if w != "" {
			warn("%s", w)
		}
		if node == nil {
			continue
		}
		if p, dup := assigned[f.Name]; dup {
			line(g, f, slug, "mapped", "same field name as another group: shares the property "+p)
			continue
		}
		name := f.Name
		if sc.IsBuiltin && have[sanitizePropName(name)] != nil {
			name = "acf_" + name // never write into a property the built-in type declares
		}
		prop := propName(name, taken)
		assigned[f.Name] = prop
		node["x-hm-group"] = g.Title
		if kind == acfKindRelation && len(f.PostTypes) > 0 {
			if target := r.schemaOf[f.PostTypes[0]]; target != "" {
				node["x-entity-schema"] = target
			}
		}
		plans = append(plans, &acfPlan{
			m:   acfMapped{Prop: prop, Field: f, Group: g, Kind: kind, Node: node, Builtin: sc.IsBuiltin, Lossy: lossy},
			src: acfSourceID(g, f, slug), status: "mapped",
		})
	}
	if sc.IsBuiltin {
		warn("ACF group %q on %q: values kept as data, built-in types cannot gain fields", g.Title, typ)
		for _, p := range plans {
			p.status, p.note = "partly", "values kept as data, built-in types cannot gain fields"
		}
	} else {
		schema := sc.Schema
		var added []string
		for _, p := range plans {
			if _, imported := done[p.src]; imported {
				// Imported by an earlier run: never added again, so a property
				// the owner deleted stays deleted. One the owner retyped since
				// is left alone and gets no values.
				if stored := have[p.m.Prop]; stored != nil && !p.adopt(stored) {
					warn("ACF field %q: the property %q of %q was changed to another type, so the field was skipped", p.m.Field.Label, p.m.Prop, slug)
					p.skip(fmt.Sprintf("the property %s was changed to another type; field skipped", p.m.Prop))
					p.status = "partly"
					continue
				}
				res.FieldsExisting++
				continue
			}
			merged, add, exist, clash, err := mergeProps(schema, []propNode{{p.m.Prop, p.m.Node}})
			switch {
			case err != nil:
				warn("ACF field %q not added to %q: %v", p.m.Field.Label, slug, err)
				p.skip("schema could not be read")
			case len(clash) > 0:
				warn("ACF field %q: %q already has a property %q of another type, so the field was skipped", p.m.Field.Label, slug, p.m.Prop)
				p.skip(fmt.Sprintf("a property %s of another type exists; field skipped", p.m.Prop))
				p.status = "partly"
			case len(exist) > 0:
				p.adopt(have[p.m.Prop])
				res.FieldsExisting++
			case len(merged) > acfSchemaBudget:
				p.skip("schema size limit reached")
			default:
				schema = merged
				added = append(added, add...)
				p.added = true
			}
		}
		if len(added) > 0 {
			if _, err := r.d.Entities.UpdateSchema(ctx, sc.ID, &entity.UpdateSchemaInput{Schema: &schema}); err != nil {
				warn("ACF group %q not added to %q: %v", g.Title, slug, err)
				for _, p := range plans {
					if p.added {
						p.added = false
						p.skip("schema could not be updated")
					}
				}
			} else {
				delete(r.schemas, slug)
				res.FieldsAdded += len(added)
			}
		}
	}
	for _, p := range plans {
		if p.status == "mapped" && p.m.Lossy {
			p.status, p.note = "partly", "nested repeaters and flexible content inside rows are stored as text"
		}
		r.acf[typ] = append(r.acf[typ], p.m)
		line(g, p.m.Field, slug, p.status, p.note)
		if p.added {
			if err := r.st.Put(ctx, r.run.ID, KindACFField, p.src, sc.ID, p.m.Prop); err != nil {
				warn("ACF field %q was added but not recorded: %v", p.m.Field.Label, err)
			}
		}
	}
}

// acfPlan is what one ACF field does on one schema.
type acfPlan struct {
	m      acfMapped
	src    string // id-map source id
	status string // mapped, partly or skipped
	note   string
	added  bool // the property is new and goes into the schema write
}

// adopt makes the values follow the property the schema has: its enum and
// maxLength replace the ones of the ACF field. It reports false when the
// stored property stores another kind of value.
func (p *acfPlan) adopt(stored map[string]any) bool {
	if !sameShape(stored, p.m.Node) {
		return false
	}
	p.m.Node = followStored(p.m.Node, stored)
	p.m.Stored = stored
	return true
}

// followStored is node with the enum and maxLength of the stored property
// instead of its own.
func followStored(node, stored map[string]any) map[string]any {
	out := make(map[string]any, len(node))
	for k, v := range node {
		out[k] = v
	}
	delete(out, "enum")
	delete(out, "maxLength")
	if l, ok := stored["enum"].([]any); ok {
		var enum []string
		for _, v := range l {
			if sv, ok := v.(string); ok {
				enum = append(enum, sv)
			}
		}
		if len(enum) == len(l) {
			out["enum"] = enum
		}
	}
	switch n := stored["maxLength"].(type) {
	case float64:
		out["maxLength"] = int(n)
	case int:
		out["maxLength"] = n
	case int64:
		out["maxLength"] = int(n)
	}
	return out
}

// schemaProps decodes the properties of a stored schema; nil when unreadable.
func schemaProps(schema jsontext.Value) map[string]map[string]any {
	var root struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := jsonx.Unmarshal(schema, &root, jsonx.StoredLoose); err != nil {
		return nil
	}
	return root.Properties
}

func (p *acfPlan) skip(note string) {
	p.m.Skipped, p.status, p.note = true, "skipped", note
}
