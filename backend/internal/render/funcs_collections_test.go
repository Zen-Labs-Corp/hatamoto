package render

import (
	"reflect"
	"testing"
)

func keysOf(gs []Group) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Key)
	}
	return out
}

func titlesOf(v any) []string {
	var out []string
	for _, it := range v.([]RelatedItem) {
		out = append(out, it.Title)
	}
	return out
}

func TestGroupBy(t *testing.T) {
	t.Parallel()
	items := []RelatedItem{
		{Title: "a", Category: "x", Attrs: map[string]any{"series": "s2"}},
		{Title: "b", Category: "y", Attrs: map[string]any{"series": "s1"}},
		{Title: "c", Category: "x", Attrs: map[string]any{"series": "s2"}},
		{Title: "d", Category: "y"},
	}
	gs := groupBy(items, "series")
	if got, want := keysOf(gs), []string{"s2", "s1", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("attrs keys = %v, want %v", got, want)
	}
	if got := titlesOf(gs[0].Items); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("group items = %v", got)
	}
	gs = groupBy(items, "Category")
	if got := keysOf(gs); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("field keys = %v", got)
	}
	if got := titlesOf(gs[1].Items); !reflect.DeepEqual(got, []string{"b", "d"}) {
		t.Errorf("field group items = %v", got)
	}

	maps := []map[string]any{{"k": "p"}, {"k": 2}, {"k": "p"}, {}}
	gs = groupBy(maps, "k")
	if got := keysOf(gs); !reflect.DeepEqual(got, []string{"p", "2", ""}) {
		t.Errorf("map keys = %v", got)
	}
	if n := len(gs[0].Items.([]map[string]any)); n != 2 {
		t.Errorf("map group len = %d", n)
	}
	anys := []any{map[string]any{"k": "z"}, &RelatedItem{Category: "z"}, nil, 5}
	if got := keysOf(groupBy(anys, "k")); !reflect.DeepEqual(got, []string{"z", ""}) {
		t.Errorf("[]any keys = %v", got)
	}

	for _, in := range []any{nil, 5, "str", struct{}{}, (*[]int)(nil), map[string]any{}} {
		if got := groupBy(in, "k"); got != nil {
			t.Errorf("groupBy(%#v) = %v, want nil", in, got)
		}
	}
	// Unexported and missing fields, odd attr names.
	type priv struct{ hidden string }
	if got := keysOf(groupBy([]priv{{"v"}}, "hidden")); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("unexported keys = %v", got)
	}
	if got := keysOf(groupBy(items, nil)); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("nil attr keys = %v", got)
	}
	// Cap.
	bigMaps := make([]map[string]any, 2500)
	for i := range bigMaps {
		bigMaps[i] = map[string]any{"k": "same"}
	}
	if n := len(groupBy(bigMaps, "k")[0].Items.([]map[string]any)); n != maxCollection {
		t.Errorf("capped len = %d", n)
	}
}

func TestSortBy(t *testing.T) {
	t.Parallel()
	items := []RelatedItem{
		{Title: "ten", Attrs: map[string]any{"order": 10}},
		{Title: "nine", Attrs: map[string]any{"order": 9}},
		{Title: "none"},
		{Title: "nine-b", Attrs: map[string]any{"order": 9.0}},
		{Title: "two", Attrs: map[string]any{"order": 2}},
	}
	orig := titlesOf(items)
	got := titlesOf(sortBy(items, "order"))
	if want := []string{"two", "nine", "nine-b", "ten", "none"}; !reflect.DeepEqual(got, want) {
		t.Errorf("numeric = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(titlesOf(items), orig) {
		t.Errorf("input was mutated: %v", titlesOf(items))
	}
	if got := titlesOf(sortBy(items, "Title")); !reflect.DeepEqual(got, []string{"nine", "nine-b", "none", "ten", "two"}) {
		t.Errorf("field = %v", got)
	}

	multi := []RelatedItem{
		{Title: "b", Attrs: map[string]any{"order": 1}},
		{Title: "a", Attrs: map[string]any{"order": 1}},
		{Title: "c", Attrs: map[string]any{"order": 0}},
	}
	if got := titlesOf(sortBy(multi, "order", "Title")); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Errorf("multi = %v", got)
	}
	// Stable on equal keys.
	if got := titlesOf(sortBy(multi, "order")); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Errorf("stable = %v", got)
	}

	maps := []map[string]any{{"n": "b"}, {}, {"n": "a"}}
	sm := sortBy(maps, "n").([]map[string]any)
	if sm[0]["n"] != "a" || sm[1]["n"] != "b" || len(sm[2]) != 0 {
		t.Errorf("maps = %v", sm)
	}

	for _, in := range []any{nil, 5, "str", struct{}{}, (*[]int)(nil)} {
		if got := sortBy(in, "k"); got != nil {
			t.Errorf("sortBy(%#v) = %v, want nil", in, got)
		}
	}
	if got := sortBy([]any{nil, 3, "x"}, "k").([]any); len(got) != 3 {
		t.Errorf("odd elements = %v", got)
	}
	bigMaps := make([]map[string]any, 2500)
	if n := len(sortBy(bigMaps, "k").([]map[string]any)); n != maxCollection {
		t.Errorf("capped len = %d", n)
	}
}

func TestPager(t *testing.T) {
	t.Parallel()
	mk := func(page, total int) *Pagination {
		p := &Pagination{Page: page, TotalPages: total, Path: "/list", HasPrev: page > 1, HasNext: page < total}
		if page > 1 {
			p.PrevURL = p.URL(page - 1)
		}
		if page < total {
			p.NextURL = p.URL(page + 1)
		}
		return p
	}
	shape := func(rows []PagerRow) string {
		var s string
		for _, r := range rows {
			switch r.Kind {
			case "gap":
				s += " .."
			case "page":
				s += " " + string(rune('0'+r.Page))
				if r.Current {
					s += "*"
				}
			default:
				s += " " + r.Kind
			}
		}
		return s
	}
	tests := []struct {
		name string
		p    *Pagination
		want string
	}{
		{"first of 9", mk(1, 9), " 1* 2 .. 9 next"},
		{"middle of 9", mk(5, 9), " prev 1 .. 4 5* 6 .. 9 next"},
		{"second of 9", mk(2, 9), " prev 1 2* 3 .. 9 next"},
		{"near end", mk(8, 9), " prev 1 .. 7 8* 9 next"},
		{"last of 9", mk(9, 9), " prev 1 .. 8 9*"},
		{"two pages", mk(1, 2), " 1* 2 next"},
	}
	for _, tc := range tests {
		if got := shape(pager(tc.p)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	rows := pager(mk(2, 9))
	if rows[0] != (PagerRow{Kind: "prev", Page: 1, Href: "/list"}) ||
		rows[2] != (PagerRow{Kind: "page", Page: 2, Href: "/list?page=2", Current: true}) ||
		rows[len(rows)-1] != (PagerRow{Kind: "next", Page: 3, Href: "/list?page=3"}) {
		t.Errorf("rows = %+v", rows)
	}
	for _, in := range []any{nil, (*Pagination)(nil), mk(1, 1), &Pagination{}, "x", Pagination{Page: 1, TotalPages: 5}} {
		if got := pager(in); got != nil {
			t.Errorf("pager(%#v) = %v, want nil", in, got)
		}
	}
}
