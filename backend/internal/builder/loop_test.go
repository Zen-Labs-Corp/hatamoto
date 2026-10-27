package builder

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func loopDoc(p map[string]any, c ...*Node) *Doc {
	return &Doc{V: 2, Nodes: []*Node{{ID: "lp", T: "loop", P: p, C: c}}}
}

func TestLoopProps(t *testing.T) {
	cases := []struct {
		name string
		p    map[string]any
		ok   bool
	}{
		{"defaults", map[string]any{"from": "query", "order": "newest", "limit": 12, "paging": "none"}, true},
		{"empty", nil, true},
		{"full", map[string]any{"schema": "post", "term": "category:news", "author": "jo-bloggs", "order": "name_desc", "limit": 48, "offset": 1000, "paging": "pages", "exclude_current": true, "context": true}, true},
		{"archive", map[string]any{"from": "archive"}, true},
		{"limit 49", map[string]any{"limit": 49}, false},
		{"limit 0", map[string]any{"limit": 0}, false},
		{"offset 1001", map[string]any{"offset": 1001}, false},
		{"offset -1", map[string]any{"offset": -1}, false},
		{"bad term", map[string]any{"term": "News"}, false},
		{"term no slug", map[string]any{"term": "category:"}, false},
		{"bad schema", map[string]any{"schema": "Post"}, false},
		{"bad author", map[string]any{"author": "Jo Bloggs"}, false},
		{"bad from", map[string]any{"from": "all"}, false},
		{"bad order", map[string]any{"order": "random"}, false},
		{"bad paging", map[string]any{"paging": "infinite"}, false},
		{"unknown prop", map[string]any{"sql": "x"}, false},
	}
	for _, c := range cases {
		err := loopDoc(c.p).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v does not wrap ErrInvalid", c.name, err)
		}
	}
}

func TestLoopNestingRefused(t *testing.T) {
	inner := &Node{ID: "in", T: "loop"}
	if err := loopDoc(nil, inner).Validate(); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("loop in loop: %v", err)
	}
	deep := &Node{ID: "c", T: "container", C: []*Node{{ID: "c2", T: "container", C: []*Node{inner}}}}
	if err := loopDoc(nil, deep).Validate(); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("loop in container in loop: %v", err)
	}
	// Sibling loops are fine.
	d := &Doc{V: 2, Nodes: []*Node{{ID: "a", T: "loop"}, {ID: "b", T: "loop"}}}
	if err := d.Validate(); err != nil {
		t.Errorf("sibling loops: %v", err)
	}
}

func TestLoopEmptyRules(t *testing.T) {
	le := func(id string) *Node { return &Node{ID: id, T: "loopempty"} }
	if err := loopDoc(nil, le("e1")).Validate(); err != nil {
		t.Errorf("one loopempty: %v", err)
	}
	if err := loopDoc(nil, le("e1"), le("e2")).Validate(); err == nil || !strings.Contains(err.Error(), "only one") {
		t.Errorf("two loopempty: %v", err)
	}
	// A loopempty with a text sibling in between is still the second one.
	if err := loopDoc(nil, le("e1"), &Node{ID: "t", T: "text"}, le("e2")).Validate(); err == nil {
		t.Error("separated second loopempty accepted")
	}
	top := &Doc{V: 2, Nodes: []*Node{le("e")}}
	if err := top.Validate(); err == nil || !strings.Contains(err.Error(), "inside a loop") {
		t.Errorf("top-level loopempty: %v", err)
	}
	// Not the direct parent: a container between loop and loopempty.
	wrapped := loopDoc(nil, &Node{ID: "c", T: "container", C: []*Node{le("e")}})
	if err := wrapped.Validate(); err == nil {
		t.Error("loopempty under a container accepted")
	}
	// One per parent, not one per doc.
	two := &Doc{V: 2, Nodes: []*Node{
		{ID: "a", T: "loop", C: []*Node{le("e1")}},
		{ID: "b", T: "loop", C: []*Node{le("e2")}}}}
	if err := two.Validate(); err != nil {
		t.Errorf("loopempty in two loops: %v", err)
	}
}

func TestLoopItemBindingsInsideLoop(t *testing.T) {
	h := &Node{ID: "h", T: "heading", B: map[string]string{"text": "loop.item.title"}}
	if err := loopDoc(nil, &Node{ID: "c", T: "container", C: []*Node{h}}).Validate(); err != nil {
		t.Errorf("loop.item inside a loop: %v", err)
	}
	if err := (&Doc{V: 2, Nodes: []*Node{h}}).Validate(); err == nil {
		t.Error("loop.item outside a loop accepted")
	}
}

func TestNewTreeLoop(t *testing.T) {
	n, err := NewTree("loop")
	if err != nil {
		t.Fatal(err)
	}
	if n.P["limit"] != float64(12) || n.P["order"] != "newest" || n.P["from"] != "query" || n.P["paging"] != "none" {
		t.Errorf("props = %v", n.P)
	}
	if len(n.C) != 2 || n.C[0].T != "container" || n.C[1].T != "loopempty" {
		t.Fatalf("children = %+v", n.C)
	}
	item := n.C[0]
	var got []string
	for _, c := range item.C {
		for k, v := range c.B {
			got = append(got, fmt.Sprintf("%s.%s=%s", c.T, k, v))
		}
	}
	want := "heading.text=loop.item.title text.text=loop.item.excerpt button.href=loop.item.url"
	if strings.Join(got, " ") != want {
		t.Errorf("bound children = %v", got)
	}
	if item.C[2].P["text"] != "Read more" {
		t.Errorf("button props = %v", item.C[2].P)
	}
	if e := n.C[1]; len(e.C) != 1 || e.C[0].P["text"] != "Nothing here yet." {
		t.Errorf("empty state = %+v", e.C)
	}
	if err := (&Doc{V: 2, Nodes: []*Node{&n}}).Validate(); err != nil {
		t.Errorf("seeded loop invalid: %v", err)
	}
	// Ids are fresh and unique across two trees.
	m, _ := NewTree("loop")
	if n.ID == m.ID || n.C[0].ID == m.C[0].ID {
		t.Error("ids repeat")
	}
}

func TestLoopEmptyNotSeedableAlone(t *testing.T) {
	if _, err := NewTree("loopempty"); err == nil {
		t.Error("NewTree(loopempty) validated outside a loop")
	}
}

func TestKindsLoop(t *testing.T) {
	byKind := map[string]KindInfo{}
	for _, k := range Kinds() {
		byKind[k.Kind] = k
	}
	l, e := byKind["loop"], byKind["loopempty"]
	if l.Group != "Data" || !l.Children || l.Hidden || l.Label != "Query loop" {
		t.Errorf("loop = %+v", l)
	}
	if !e.Hidden || e.OnlyIn != "loop" || !e.Children {
		t.Errorf("loopempty = %+v", e)
	}
	if len(l.Seed) != 2 || len(l.Seed[0].C) != 3 || l.Seed[0].C[0].B["text"] != "loop.item.title" {
		t.Fatalf("seed = %+v", l.Seed)
	}
	// Seed is a fresh deep copy.
	l.Seed[0].C[0].B["text"] = "x"
	for _, k := range Kinds() {
		if k.Kind == "loop" && k.Seed[0].C[0].B["text"] != "loop.item.title" {
			t.Error("Seed shared")
		}
	}
	for _, p := range l.Props {
		if p.Key == "schema" && p.Widget != "datalist:schemas" {
			t.Errorf("schema widget = %q", p.Widget)
		}
		if p.Key == "term" && p.Widget != "datalist:terms" {
			t.Errorf("term widget = %q", p.Widget)
		}
		if p.Bind != "" {
			t.Errorf("loop prop %s is bindable", p.Key)
		}
	}
}

func TestLoopDocIsNotV1Expressible(t *testing.T) {
	if V1Expressible(loopDoc(nil)) {
		t.Error("a loop doc is v1 expressible")
	}
}

func TestSeedLimits(t *testing.T) {
	deep := `{"x-hm":{"label":"A","group":"Content","children":true,
		"seed":[{"t":"container","c":[{"t":"container","c":[{"t":"container","c":[{"t":"text"}]}]}]}]}}`
	if _, err := parseWidget("a", []byte(deep)); err == nil {
		t.Error("seed nested 4 deep accepted")
	}
	ok := `{"x-hm":{"label":"A","group":"Content","children":true,
		"seed":[{"t":"container","c":[{"t":"container","c":[{"t":"text"}]}]}]}}`
	if _, err := parseWidget("a", []byte(ok)); err != nil {
		t.Errorf("seed nested 3 deep: %v", err)
	}
}
