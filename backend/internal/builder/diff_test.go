package builder

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func nd(id, t string, c ...*Node) *Node { return &Node{ID: id, T: t, C: c} }

func txt(id, text string) *Node {
	return &Node{ID: id, T: "text", P: map[string]any{"text": text}}
}

func doc(ns ...*Node) *Doc { return &Doc{V: 2, Nodes: ns} }

func ops(d *DocDiff) string {
	var parts []string
	for _, c := range d.Changes {
		parts = append(parts, c.ID+":"+c.Op)
	}
	return strings.Join(parts, " ")
}

func TestDiffDocsTable(t *testing.T) {
	cases := []struct {
		name string
		a, b *Doc
		want string
	}{
		{"identical", doc(txt("a", "x"), nd("c", "container", txt("b", "y"))), doc(txt("a", "x"), nd("c", "container", txt("b", "y"))), ""},
		{"nil docs", nil, nil, ""},
		{"nil old", nil, doc(txt("a", "x")), "a:added"},
		{"prop", doc(txt("a", "x")), doc(txt("a", "z")), "a:changed"},
		{"insert before sibling", doc(txt("a", "1"), txt("b", "2")), doc(txt("x", "0"), txt("a", "1"), txt("b", "2")), "x:added"},
		{"insert between", doc(txt("a", "1"), txt("b", "2")), doc(txt("a", "1"), txt("x", "0"), txt("b", "2")), "x:added"},
		{"remove sibling", doc(txt("a", "1"), txt("b", "2"), txt("c", "3")), doc(txt("a", "1"), txt("c", "3")), "b:removed"},
		{"reorder first to last", doc(txt("a", "1"), txt("b", "2"), txt("c", "3"), txt("d", "4")), doc(txt("b", "2"), txt("c", "3"), txt("d", "4"), txt("a", "1")), "a:moved"},
		{"reorder last to first", doc(txt("a", "1"), txt("b", "2"), txt("c", "3")), doc(txt("c", "3"), txt("a", "1"), txt("b", "2")), "c:moved"},
		{"move to other parent",
			doc(nd("p", "container", txt("a", "1")), nd("q", "container")),
			doc(nd("p", "container"), nd("q", "container", txt("a", "1"))), "a:moved"},
		{"add subtree", doc(nd("p", "container")), doc(nd("p", "container", nd("n", "container", txt("k", "1"), txt("l", "2")))), "n:added"},
		{"moved and changed", doc(txt("a", "1"), txt("b", "2"), txt("c", "3")), doc(txt("c", "33"), txt("a", "1"), txt("b", "2")), "c:moved"},
	}
	for _, c := range cases {
		got := ops(DiffDocs(c.a, c.b))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDiffRemoveSubtree(t *testing.T) {
	a := doc(nd("stats", "container", txt("a", "1"), nd("in", "container", txt("b", "2"))), txt("z", "z"))
	b := doc(txt("z", "z"))
	d := DiffDocs(a, b)
	if ops(d) != "stats:removed" {
		t.Fatalf("got %q", ops(d))
	}
	c := d.Changes[0]
	if c.Subtree != 3 || c.Parent != "" || c.Index != 0 {
		t.Errorf("change = %+v", c)
	}
}

func TestDiffAddSubtree(t *testing.T) {
	a := doc(nd("p", "container"))
	b := doc(nd("p", "container", nd("n", "container", txt("k", "1"), txt("l", "2"))))
	c := DiffDocs(a, b).Changes[0]
	if c.ID != "n" || c.Op != DiffAdded || c.Subtree != 2 || c.Parent != "p" || c.Index != 0 {
		t.Errorf("change = %+v", c)
	}
}

// A node that already existed and was moved into an added container has its
// own Moved row and is not counted in the container's Added size.
func TestDiffAddedCountExcludesMovedNodes(t *testing.T) {
	a := doc(txt("x", "1"), txt("y", "2"))
	b := doc(nd("n", "container", txt("x", "1"), txt("k", "3")), txt("y", "2"))
	d := DiffDocs(a, b)
	if ops(d) != "n:added x:moved" {
		t.Fatalf("got %q", ops(d))
	}
	if c := d.Changes[0]; c.Subtree != 1 {
		t.Errorf("Subtree = %d, want 1 (only k is new)", c.Subtree)
	}
}

func TestDiffFields(t *testing.T) {
	a := doc(&Node{ID: "h", T: "heading",
		P: map[string]any{"text": "Welcome", "level": float64(2), "gone": "x"},
		S: map[string]map[string]string{"base": {"pt": "space-4"}, "md": {"pt": "space-2"}},
		B: map[string]string{"text": "post.title"}})
	b := doc(&Node{ID: "h", T: "heading",
		P: map[string]any{"text": "Hello", "level": float64(2), "new": true},
		S: map[string]map[string]string{"base": {"pt": "space-4"}, "md": {"pt": "space-8", "mt": "space-1"}},
		B: map[string]string{"text": "post.excerpt", "level": "x"}})
	c := DiffDocs(a, b).Changes[0]
	if c.Op != DiffChanged || c.Label != "heading" || c.Hint != "Hello" {
		t.Fatalf("change = %+v", c)
	}
	wantP := []FieldChange{
		{Key: "gone", Kind: DiffRemoved, Old: "x"},
		{Key: "new", Kind: DiffAdded, New: "true"},
		{Key: "text", Kind: DiffChanged, Old: "Welcome", New: "Hello"},
	}
	if !reflect.DeepEqual(c.Props, wantP) {
		t.Errorf("props = %+v", c.Props)
	}
	wantS := []FieldChange{
		{Key: "mt", BP: "md", Kind: DiffAdded, New: "space-1"},
		{Key: "pt", BP: "md", Kind: DiffChanged, Old: "space-2", New: "space-8"},
	}
	if !reflect.DeepEqual(c.Styles, wantS) {
		t.Errorf("styles = %+v", c.Styles)
	}
	wantB := []FieldChange{
		{Key: "level", Kind: DiffAdded, New: "x"},
		{Key: "text", Kind: DiffChanged, Old: "post.title", New: "post.excerpt"},
	}
	if !reflect.DeepEqual(c.Binds, wantB) {
		t.Errorf("binds = %+v", c.Binds)
	}
}

func TestDiffTypeChange(t *testing.T) {
	c := DiffDocs(doc(nd("a", "text")), doc(nd("a", "heading"))).Changes[0]
	if c.Op != DiffChanged || c.TypeFrom != "text" || c.T != "heading" {
		t.Errorf("change = %+v", c)
	}
}

func TestDiffMovedKeepsFields(t *testing.T) {
	a := doc(txt("a", "1"), txt("b", "2"), txt("c", "3"))
	b := doc(txt("c", "33"), txt("a", "1"), txt("b", "2"))
	c := DiffDocs(a, b).Changes[0]
	if c.Op != DiffMoved || len(c.Props) != 1 || c.Props[0].New != "33" {
		t.Errorf("change = %+v", c)
	}
}

func TestDiffTruncates(t *testing.T) {
	var as, bs []*Node
	for i := 0; i < 250; i++ {
		as = append(as, txt(fmt.Sprintf("n%03d", i), "a"))
		bs = append(bs, txt(fmt.Sprintf("n%03d", i), "b"))
	}
	d := DiffDocs(doc(as...), doc(bs...))
	if len(d.Changes) != 200 || d.Truncated != 50 {
		t.Fatalf("len=%d truncated=%d", len(d.Changes), d.Truncated)
	}
	if d.Changes[0].ID != "n000" {
		t.Errorf("first = %s", d.Changes[0].ID)
	}
}

func TestDiffLabels(t *testing.T) {
	d := DiffDocs(nil, doc(nd("a", "container"), nd("b", "acme_x"), txt("c", "t")))
	got := map[string]string{}
	for _, c := range d.Changes {
		got[c.ID] = c.Label
	}
	if got["b"] != "acme_x" {
		t.Errorf("unknown kind label = %q", got["b"])
	}
	if got["c"] == "" || got["a"] == "" {
		t.Errorf("labels = %v", got)
	}
}

func TestDiffHintTruncates(t *testing.T) {
	long := strings.Repeat("é", 60)
	c := DiffDocs(nil, doc(txt("a", long))).Changes[0]
	if want := strings.Repeat("é", 40) + "…"; c.Hint != want {
		t.Errorf("hint = %q", c.Hint)
	}
}

func TestDiffOrderAndDeterminism(t *testing.T) {
	a := doc(txt("a", "1"), txt("gone", "g"), nd("p", "container", txt("b", "2")))
	b := doc(txt("a", "1x"), nd("p", "container", txt("b", "2x"), txt("new", "n")))
	first := ops(DiffDocs(a, b))
	if first != "a:changed b:changed new:added gone:removed" {
		t.Fatalf("order = %q", first)
	}
	for i := 0; i < 20; i++ {
		if got := ops(DiffDocs(a, b)); got != first {
			t.Fatalf("run %d: %q != %q", i, got, first)
		}
	}
}

func TestDiffStyleBreakpointOrder(t *testing.T) {
	a := doc(&Node{ID: "a", T: "container"})
	b := doc(&Node{ID: "a", T: "container", S: map[string]map[string]string{
		"sm": {"pt": "space-1"}, "base": {"pt": "space-1"}, "md": {"pt": "space-1"}}})
	var bps []string
	for _, f := range DiffDocs(a, b).Changes[0].Styles {
		bps = append(bps, f.BP)
	}
	if !reflect.DeepEqual(bps, []string{"base", "md", "sm"}) {
		t.Errorf("bps = %v", bps)
	}
}

// State changes sort after the plain key of their breakpoint, in sKeyOrder.
func TestDiffStateKeysOrder(t *testing.T) {
	a := doc(&Node{ID: "h", T: "heading", S: map[string]map[string]string{"sm": {"bg": "primary"}}})
	b := doc(&Node{ID: "h", T: "heading", S: map[string]map[string]string{
		"sm": {"bg": "accent"}, "md:hover": {"op": "0.5"}, "base:focus": {"bc": "accent"}, "base:hover": {"op": "0.5"}, "base": {"op": "0.4"}, "zz": {}}})
	var got []string
	for _, f := range DiffDocs(a, b).Changes[0].Styles {
		got = append(got, f.BP+"."+f.Key)
	}
	if want := "base.op base:hover.op base:focus.bc md:hover.op sm.bg"; strings.Join(got, " ") != want {
		t.Errorf("order = %s, want %s", strings.Join(got, " "), want)
	}
}

func TestDiffInlineHTML(t *testing.T) {
	a := doc(&Node{ID: "h", T: "heading", P: map[string]any{"html": "Hello world"}})
	b := doc(&Node{ID: "h", T: "heading", P: map[string]any{"html": "Hello <strong>world</strong> &amp; co"}})
	c := DiffDocs(a, b).Changes[0]
	want := []FieldChange{{Key: "html", Kind: DiffChanged, Old: "Hello world", New: "Hello world & co"}}
	if !reflect.DeepEqual(c.Props, want) {
		t.Errorf("props = %+v", c.Props)
	}
	if c.Hint != "Hello world & co" {
		t.Errorf("hint = %q, want the stripped html", c.Hint)
	}

	b = doc(&Node{ID: "h", T: "heading", P: map[string]any{"html": "Hello <strong>world</strong>"}})
	c = DiffDocs(a, b).Changes[0]
	want = []FieldChange{{Key: "html", Kind: DiffChanged, Old: "Hello world", New: "Hello world", Note: "formatting changed"}}
	if !reflect.DeepEqual(c.Props, want) {
		t.Errorf("formatting only: props = %+v", c.Props)
	}

	// Other kinds keep the raw value, and text wins the hint.
	x := doc(&Node{ID: "r", T: "richtext", P: map[string]any{"html": "<p>a</p>"}})
	y := doc(&Node{ID: "r", T: "richtext", P: map[string]any{"html": "<p>b</p>"}})
	if p := DiffDocs(x, y).Changes[0].Props[0]; p.Old != "<p>a</p>" || p.Note != "" {
		t.Errorf("richtext = %+v", p)
	}
	n := &Node{ID: "t", T: "text", P: map[string]any{"text": "plain", "html": "<b>other</b>"}}
	if got := hintOf(n); got != "plain" {
		t.Errorf("hintOf = %q", got)
	}
}
