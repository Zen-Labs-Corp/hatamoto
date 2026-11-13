package render

import (
	"reflect"
	"sort"
	"time"
)

// maxCollection bounds how many elements groupBy and sortBy process.
const maxCollection = 1000

// Group is one groupBy result: the key's text and the items sharing it, as a
// slice of the input's own type.
type Group struct {
	Key   string
	Items any
}

// sliceOf returns v as a slice value, following pointers and interfaces.
func sliceOf(v any) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return reflect.Value{}, false
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return reflect.Value{}, false
	}
	return rv, true
}

// elemAttr reads attr from an element: a string-keyed map entry, an exported
// struct field of that name, then the entry of the struct's Attrs map.
func elemAttr(el reflect.Value, attr string) (any, bool) {
	for el.IsValid() && (el.Kind() == reflect.Pointer || el.Kind() == reflect.Interface) {
		if el.IsNil() {
			return nil, false
		}
		el = el.Elem()
	}
	if !el.IsValid() {
		return nil, false
	}
	switch el.Kind() {
	case reflect.Map:
		if el.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		return valueAny(el.MapIndex(reflect.ValueOf(attr).Convert(el.Type().Key())))
	case reflect.Struct:
		if f, ok := el.Type().FieldByName(attr); ok && f.IsExported() && len(f.Index) == 1 {
			return valueAny(el.FieldByIndex(f.Index))
		}
		if f, ok := el.Type().FieldByName("Attrs"); ok && f.IsExported() && f.Type.Kind() == reflect.Map &&
			f.Type.Key().Kind() == reflect.String {
			m := el.FieldByIndex(f.Index)
			return valueAny(m.MapIndex(reflect.ValueOf(attr).Convert(f.Type.Key())))
		}
	}
	return nil, false
}

func valueAny(v reflect.Value) (any, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return nil, false
	}
	if (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && v.IsNil() {
		return nil, false
	}
	return v.Interface(), true
}

// groupBy groups the elements of a slice by an attribute, in first-seen order.
// Elements without the attribute share the group with key "".
func groupBy(items, attr any) []Group {
	rv, ok := sliceOf(items)
	if !ok {
		return nil
	}
	name := toStr(attr)
	var groups []Group
	var slices []reflect.Value
	index := map[string]int{}
	for i := 0; i < rv.Len() && i < maxCollection; i++ {
		el := rv.Index(i)
		v, _ := elemAttr(el, name)
		key := attrText(v)
		gi, seen := index[key]
		if !seen {
			gi = len(groups)
			index[key] = gi
			groups = append(groups, Group{Key: key})
			slices = append(slices, reflect.MakeSlice(rv.Type(), 0, 1))
		}
		slices[gi] = reflect.Append(slices[gi], el)
	}
	for i := range groups {
		groups[i].Items = slices[i].Interface()
	}
	return groups
}

// sortBy returns a stably sorted copy of a slice, ordered by attr and then by
// each further attribute name. Numbers compare numerically, anything else as
// text; elements missing the attribute come last.
func sortBy(items, attr any, more ...any) any {
	rv, ok := sliceOf(items)
	if !ok {
		return nil
	}
	n := min(rv.Len(), maxCollection)
	names := append([]any{attr}, more...)
	type row struct {
		el   reflect.Value
		keys []any // nil entry: missing
	}
	rows := make([]row, n)
	for i := range rows {
		rows[i].el = rv.Index(i)
		for _, name := range names {
			v, _ := elemAttr(rows[i].el, toStr(name))
			rows[i].keys = append(rows[i].keys, v)
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		for k := range names {
			if c := compareAttr(rows[a].keys[k], rows[b].keys[k]); c != 0 {
				return c < 0
			}
		}
		return false
	})
	out := reflect.MakeSlice(rv.Type(), 0, n)
	for _, r := range rows {
		out = reflect.Append(out, r.el)
	}
	return out.Interface()
}

// compareAttr orders two attribute values; a nil (missing) value is the last.
func compareAttr(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	if x, ok := asTime(a); ok {
		if y, ok := asTime(b); ok {
			return x.Compare(y)
		}
	}
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	}
	sa, sb := attrText(a), attrText(b)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	}
	return 0
}

func number(v any) (float64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}

// PagerRow is one row of the pager func: Kind is "prev", "page", "gap" or
// "next". Gap rows have no Page or Href.
type PagerRow struct {
	Kind    string
	Page    int
	Href    string
	Current bool
}

// pager lists the rows of a pagination control for a *Pagination: previous,
// the first and last page, the current page and one either side, gaps between
// non-adjacent pages, next. One page, nil or another type give no rows.
func pager(v any) []PagerRow {
	p, ok := v.(*Pagination)
	if !ok || p == nil || p.TotalPages < 2 {
		return nil
	}
	var out []PagerRow
	if p.Page > 1 {
		out = append(out, PagerRow{Kind: "prev", Page: p.Page - 1, Href: cmpOr(p.PrevURL, p.URL(p.Page-1))})
	}
	last := 0
	for n := 1; n <= p.TotalPages; n++ {
		if n != 1 && n != p.TotalPages && (n < p.Page-1 || n > p.Page+1) {
			continue
		}
		if last != 0 && n != last+1 {
			out = append(out, PagerRow{Kind: "gap"})
		}
		out = append(out, PagerRow{Kind: "page", Page: n, Href: p.URL(n), Current: n == p.Page})
		last = n
	}
	if p.Page < p.TotalPages {
		out = append(out, PagerRow{Kind: "next", Page: p.Page + 1, Href: cmpOr(p.NextURL, p.URL(p.Page+1))})
	}
	return out
}

// cmpOr returns a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t != nil {
			return *t, true
		}
	}
	return time.Time{}, false
}

// attrText is the text of a group or sort key: toStr, plus "true"/"false" for
// bools and a UTC RFC 3339 form for times.
func attrText(v any) string {
	if t, ok := asTime(v); ok {
		return t.UTC().Format(time.RFC3339Nano)
	}
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return toStr(v)
}
