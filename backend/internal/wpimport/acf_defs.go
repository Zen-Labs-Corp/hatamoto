package wpimport

import (
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxACFValue is the longest ACF meta value kept (the generic meta cap is 2048
// bytes; long wysiwyg and repeater text must survive).
const maxACFValue = 1 << 20

var (
	acfKeyRe    = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^field_[0-9a-f]{13}$`) })
	acfDateRe   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^\d{8}$`) })
	acfRowKeyRe = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`_\d+_`) })
	acfNumberRe = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^-?\d+(\.\d+)?$`) })
	acfEmailRe  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`) })
	acfMaxDepth = 8
	acfMaxRows  = 500
)

// maxMetaKey is the longest meta key WordPress stores (the column is varchar(255)).
const maxMetaKey = 255

// isACFKey reports whether the meta key k holds an ACF value: its reference
// key "_k" names a field, or it is a row key "<parent>_<i>_<sub>" whose parent
// (any prefix before a row segment) has such a reference. Every prefix is
// checked once, so the work is bounded by the key length, which is capped.
func isACFKey(meta map[string]string, k string) bool {
	if len(k) > maxMetaKey {
		return false
	}
	if acfKeyRe().MatchString(meta["_"+k]) {
		return true
	}
	for _, loc := range acfRowKeyRe().FindAllStringIndex(k, -1) {
		if loc[0] > 0 && acfKeyRe().MatchString(meta["_"+k[:loc[0]]]) {
			return true
		}
	}
	return false
}

// keepMeta says whether a meta value is stored in Item.Meta.
func keepMeta(meta map[string]string, k, v string) bool {
	if len(v) < 2048 {
		return true
	}
	return len(v) <= maxACFValue && isACFKey(meta, k)
}

// cf7FromWXR reads a wpcf7_contact_form item.
func cf7FromWXR(w wxrItem) CF7Form {
	hash := w.meta["_hash"]
	if len(hash) > 7 {
		hash = hash[:7]
	}
	return CF7Form{
		ID: w.fields["post_id"], Hash: hash, Slug: w.fields["post_name"], Title: htmlText(w.fields["title"]),
		Template: w.meta["_form"], HasMail: strings.TrimSpace(w.meta["_mail"]) != "",
	}
}

type acfDefNode struct {
	field    ACFField
	parent   string
	layout   string
	settings any
}

// buildACFGroups turns the acf-field-group and acf-field items into groups
// with their field trees. Bad settings never stop the build: the field keeps
// what could be read.
func buildACFGroups(groups, fields []wxrItem) []ACFGroup {
	byParent := map[string][]acfDefNode{}
	for _, w := range fields {
		f := w.fields
		set, _ := phpUnserialize(f["content"])
		n := acfDefNode{parent: f["post_parent"], settings: set}
		n.layout = phpText(set, "parent_layout")
		n.field = ACFField{
			ID: f["post_id"], Key: f["post_name"], Name: f["excerpt"], Label: htmlText(f["title"]), Type: phpText(set, "type"),
			Multiple: phpText(set, "multiple") == "1", PostTypes: phpStrings(phpText(set, "post_type")),
		}
		if a, ok := phpGet(set, "post_type").(*phpArray); ok {
			n.field.PostTypes = n.field.PostTypes[:0]
			for _, k := range a.Keys {
				if t, ok := phpScalar(a.Vals[k]); ok && t != "" {
					n.field.PostTypes = append(n.field.PostTypes, t)
				}
			}
		}
		n.field.Order, _ = strconv.Atoi(f["menu_order"])
		if a, ok := phpGet(set, "choices").(*phpArray); ok {
			for _, k := range a.Keys {
				if label, ok := phpScalar(a.Vals[k]); ok {
					n.field.Choices = append(n.field.Choices, ACFChoice{Value: k, Label: label})
				}
			}
		}
		byParent[n.parent] = append(byParent[n.parent], n)
	}
	for _, list := range byParent {
		sort.SliceStable(list, func(i, j int) bool { return list[i].field.Order < list[j].field.Order })
	}
	var build func(parent string, layout string, depth int, seen map[string]bool) []ACFField
	build = func(parent, layout string, depth int, seen map[string]bool) []ACFField {
		var out []ACFField
		for _, n := range byParent[parent] {
			if n.layout != layout || seen[n.field.ID] {
				continue
			}
			f := n.field
			if depth < acfMaxDepth {
				seen[f.ID] = true
				switch f.Type {
				case "flexible_content":
					if a, ok := phpGet(n.settings, "layouts").(*phpArray); ok {
						for _, k := range a.Keys {
							l := a.Vals[k]
							key := firstNonEmpty(phpText(l, "key"), k)
							f.Layouts = append(f.Layouts, ACFLayout{Key: key, Name: phpText(l, "name"), Label: phpText(l, "label"),
								Sub: build(f.ID, key, depth+1, seen)})
						}
					}
				default:
					f.Sub = build(f.ID, "", depth+1, seen)
				}
			}
			out = append(out, f)
		}
		return out
	}
	var out []ACFGroup
	sorted := append([]wxrItem(nil), groups...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return atoi(sorted[i].fields["menu_order"]) < atoi(sorted[j].fields["menu_order"])
	})
	for _, w := range sorted {
		f := w.fields
		g := ACFGroup{ID: f["post_id"], Key: f["post_name"], Title: htmlText(f["title"])}
		set, _ := phpUnserialize(f["content"])
		g.Types, g.OtherLocations = acfLocations(set)
		g.Fields = build(g.ID, "", 0, map[string]bool{})
		out = append(out, g)
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// acfLocations reads the location rules of a group: post types it is shown on
// and the parameters of rules the import does not map.
func acfLocations(set any) (types, other []string) {
	add := func(list []string, v string) []string {
		for _, x := range list {
			if x == v {
				return list
			}
		}
		return append(list, v)
	}
	ors, _ := phpGet(set, "location").(*phpArray)
	if ors == nil {
		return nil, nil
	}
	for _, ok := range ors.Keys {
		ands, _ := ors.Vals[ok].(*phpArray)
		if ands == nil {
			continue
		}
		for _, rk := range ands.Keys {
			rule := ands.Vals[rk]
			param, op, val := phpText(rule, "param"), phpText(rule, "operator"), phpText(rule, "value")
			switch {
			case param == "post_type" && op == "==" && val != "":
				types = add(types, val)
			case param == "page_template" && op == "==":
				types = add(types, "page")
			case param != "":
				other = add(other, param)
			}
		}
	}
	return types, other
}

// inferACFGroups guesses field groups from the meta of items when the export
// has no definitions: keys whose "_<key>" reference names a field. media holds
// the attachment ids of the export.
func inferACFGroups(items []Item, media map[string]bool) []ACFGroup {
	var order []string
	byType := map[string]*ACFGroup{}
	index := map[string]map[string]int{}
	for _, it := range items {
		keys := make([]string, 0, len(it.Meta))
		for k := range it.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !acfKeyRe().MatchString(it.Meta["_"+k]) || isRowKey(it.Meta, k) {
				continue
			}
			g := byType[it.Type]
			if g == nil {
				g = &ACFGroup{ID: "guess:" + it.Type, Title: "Custom fields (" + it.Type + ")", Types: []string{it.Type}, Guessed: true}
				byType[it.Type] = g
				index[it.Type] = map[string]int{}
				order = append(order, it.Type)
			}
			f := guessACFField(it.Meta, keys, k, k, media, 0)
			if i, seen := index[it.Type][k]; seen {
				if it.Meta[k] != "" && g.Fields[i].Type == "text" && len(g.Fields[i].Sub) == 0 {
					f.Order = g.Fields[i].Order
					g.Fields[i] = f
				}
				continue
			}
			f.Order = len(g.Fields)
			index[it.Type][k] = len(g.Fields)
			g.Fields = append(g.Fields, f)
		}
	}
	var out []ACFGroup
	for _, t := range order {
		out = append(out, *byType[t])
	}
	return out
}

// isRowKey reports whether k is a row key of an ACF parent.
func isRowKey(meta map[string]string, k string) bool {
	for _, loc := range acfRowKeyRe().FindAllStringIndex(k, -1) {
		if loc[0] > 0 && isACFKey(meta, k[:loc[0]]) {
			return true
		}
	}
	return false
}

// guessACFField infers one field from the value at key. name is the field
// name, keys the sorted meta keys of the item.
func guessACFField(meta map[string]string, keys []string, key, name string, media map[string]bool, depth int) ACFField {
	f := ACFField{Key: meta["_"+key], Name: name, Label: name}
	v := meta[key]
	rows := atoi(v)
	serialized := strings.HasPrefix(v, "a:")
	if depth < acfMaxDepth && (serialized || rows > 0) {
		var layouts []string
		if serialized {
			layouts = phpStrings(v)
			rows = len(layouts)
		}
		if rows > acfMaxRows {
			rows = acfMaxRows
		}
		if subs, ok := guessRows(meta, keys, key, rows, layouts, media, depth); ok {
			if layouts != nil {
				f.Type = "flexible_content"
				f.Layouts = subs
			} else {
				f.Type = "repeater"
				f.Sub = subs[0].Sub
			}
			return f
		}
	}
	f.Type, f.Choices, f.Multiple = guessACFType(v, media)
	return f
}

// guessRows reads the sub fields of rows 0..rows-1 under key. For a repeater
// (layouts == nil) it returns one pseudo layout holding the union of sub fields.
func guessRows(meta map[string]string, keys []string, key string, rows int, layouts []string, media map[string]bool, depth int) ([]ACFLayout, bool) {
	var out []ACFLayout
	at := map[string]int{}
	found := false
	for i := range rows {
		prefix := key + "_" + strconv.Itoa(i) + "_"
		name := ""
		if layouts != nil {
			name = layouts[i]
		}
		li, ok := at[name]
		if !ok {
			li = len(out)
			at[name] = li
			out = append(out, ACFLayout{Key: name, Name: name, Label: name})
		}
		have := map[string]bool{}
		for _, s := range out[li].Sub {
			have[s.Name] = true
		}
		for _, k := range keys {
			if !strings.HasPrefix(k, prefix) || !acfKeyRe().MatchString(meta["_"+k]) {
				continue
			}
			sub := k[len(prefix):]
			if nestedRow(meta, prefix, sub) || have[sub] {
				continue
			}
			have[sub] = true
			found = true
			sf := guessACFField(meta, keys, k, sub, media, depth+1)
			sf.Order = len(out[li].Sub)
			out[li].Sub = append(out[li].Sub, sf)
		}
	}
	return out, found
}

// nestedRow reports whether sub (below prefix) is a row key of a deeper ACF
// parent rather than a direct child.
func nestedRow(meta map[string]string, prefix, sub string) bool {
	for _, loc := range acfRowKeyRe().FindAllStringIndex(sub, -1) {
		if loc[0] > 0 && isACFKey(meta, prefix+sub[:loc[0]]) {
			return true
		}
	}
	return false
}

// guessACFType infers a scalar field type from one value.
func guessACFType(v string, media map[string]bool) (typ string, choices []ACFChoice, multiple bool) {
	switch {
	case v == "":
		return "text", nil, false
	case strings.HasPrefix(v, "a:"):
		ids := phpStrings(v)
		allID, allMedia := len(ids) > 0, len(ids) > 0
		for _, id := range ids {
			if !acfNumberRe().MatchString(id) {
				allID = false
			}
			if !media[id] {
				allMedia = false
			}
		}
		switch {
		case allMedia:
			return "gallery", nil, false
		case allID:
			return "relationship", nil, true
		}
		return "checkbox", nil, true
	case media[v]:
		return "image", nil, false
	case acfDateRe().MatchString(v):
		return "date_picker", nil, false
	case acfNumberRe().MatchString(v):
		return "number", nil, false
	case strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://"):
		return "url", nil, false
	case acfEmailRe().MatchString(v):
		return "email", nil, false
	case strings.Contains(v, "\n"):
		return "textarea", nil, false
	}
	return "text", nil, false
}

// htmlText unescapes the entities WordPress leaves in titles.
func htmlText(s string) string { return strings.TrimSpace(html.UnescapeString(s)) }
