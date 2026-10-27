package builder

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ptrBool(b bool) *bool { return &b }

func sampleSections() []Section {
	return []Section{
		{ID: "s-b", Type: "hero", Title: "Second", Content: map[string]any{"h": "B", "list": []any{1.0, map[string]any{"x": "y"}}}, SortOrder: 20},
		{ID: "s-a", Type: "text", Content: map[string]any{"body": "<p>A</p>"}, SortOrder: 10},
		{ID: "s-c", Type: "cta", Content: map[string]any{}, IsVisible: ptrBool(false), SortOrder: 30},
		{ID: "s-d", Type: "empty", SortOrder: 30}, // nil content, tie on sort_order
	}
}

func TestFromSectionsRenderOrderAndValid(t *testing.T) {
	d := FromSections(sampleSections())
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range Components(d) {
		order = append(order, c.ID)
	}
	if want := []string{"s-a", "s-b", "s-c", "s-d"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	if !V1Expressible(d) {
		t.Fatal("converted doc not expressible")
	}
}

func TestRoundTripUpToRenumbering(t *testing.T) {
	in := sampleSections()
	out, ok := ToSections(FromSections(in))
	if !ok || len(out) != len(in) {
		t.Fatalf("ok=%v len=%d", ok, len(out))
	}
	want := []Section{in[1], in[0], in[2], in[3]} // render order
	for i := range want {
		want[i].SortOrder = i
		if want[i].Content == nil {
			want[i].Content = map[string]any{}
		}
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("round trip\n got %#v\nwant %#v", out, want)
	}
	// A second pass is stable.
	out2, _ := ToSections(FromSections(out))
	if !reflect.DeepEqual(out, out2) {
		t.Fatal("round trip not a fixed point")
	}
}

func TestFromSectionsDeterministic(t *testing.T) {
	in := sampleSections()
	in = append(in, Section{ID: "bad id!", Type: "x", SortOrder: 1}, Section{Type: "y", SortOrder: 1})
	a, _ := json.Marshal(FromSections(in))
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(FromSections(in))
		if !bytes.Equal(a, b) {
			t.Fatal("FromSections is not deterministic")
		}
	}
}

func TestFromSectionsUniqueIDs(t *testing.T) {
	long := strings.Repeat("l", 64)
	in := []Section{
		{ID: "dup", Type: "a"}, {ID: "dup", Type: "a"}, {ID: "dup", Type: "b"},
		{ID: "", Type: "a"}, {ID: "", Type: "a"}, {ID: "has space", Type: "a"},
		{ID: long, Type: "a"}, {ID: "c-dup", Type: "a"}, {ID: "c-" + long[:10], Type: "a"}, {ID: long[:10], Type: "a"},
	}
	d := FromSections(in)
	if err := d.Validate(); err != nil {
		t.Fatalf("invalid doc: %v", err)
	}
	if d.Count() != 2*len(in) {
		t.Fatalf("count %d", d.Count())
	}
	if !V1Expressible(d) {
		t.Fatal("not expressible")
	}
	// Valid, unique section ids are kept as component ids.
	if n, _ := d.Find("dup"); n == nil || n.T != "component" {
		t.Fatal("first section id not kept")
	}
	if n, _ := d.Find(long); n == nil {
		t.Fatal("64-char id not kept")
	}
}

func TestFromSectionsDoesNotAliasInput(t *testing.T) {
	in := sampleSections()
	d := FromSections(in)
	in[0].Content["h"] = "mutated"
	n, _ := d.Find("s-b")
	if n.P["content"].(map[string]any)["h"] != "B" {
		t.Fatal("doc aliases section content")
	}
	out, _ := ToSections(d)
	out[1].Content["h"] = "mutated"
	if n.P["content"].(map[string]any)["h"] != "B" {
		t.Fatal("ToSections aliases doc content")
	}
}

func TestEmptyAndHidden(t *testing.T) {
	d := FromSections(nil)
	if err := d.Validate(); err != nil || len(d.Nodes) != 0 {
		t.Fatalf("%v %d", err, len(d.Nodes))
	}
	if s, ok := ToSections(d); !ok || len(s) != 0 {
		t.Fatal("empty doc")
	}
	d = FromSections(sampleSections())
	n, _ := d.Find("c-s-c")
	if n.P["hidden"] != true {
		t.Fatalf("hidden not kept: %v", n.P)
	}
	if n, _ := d.Find("c-s-a"); n.P["hidden"] != nil {
		t.Fatal("visible section marked hidden")
	}
}

func TestNotExpressible(t *testing.T) {
	cases := map[string]string{
		"top-level widget":  `{"v":2,"nodes":[{"id":"a","t":"heading","p":{"text":"x"}}]}`,
		"div tag":           `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"div"},"c":[{"id":"b","t":"component","p":{"type":"x"}}]}]}`,
		"no tag":            `{"v":2,"nodes":[{"id":"a","t":"container","c":[{"id":"b","t":"component","p":{"type":"x"}}]}]}`,
		"layout prop":       `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section","layout":"grid"},"c":[{"id":"b","t":"component","p":{"type":"x"}}]}]}`,
		"styled container":  `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"s":{"base":{"pt":"space-1"}},"c":[{"id":"b","t":"component","p":{"type":"x"}}]}]}`,
		"styled component":  `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"c":[{"id":"b","t":"component","p":{"type":"x"},"s":{"base":{"pt":"space-1"}}}]}]}`,
		"two children":      `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"c":[{"id":"b","t":"component","p":{"type":"x"}},{"id":"c","t":"component","p":{"type":"y"}}]}]}`,
		"widget child":      `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"c":[{"id":"b","t":"text"}]}]}`,
		"no child":          `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"}}]}`,
		"component hidden":  `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"c":[{"id":"b","t":"component","p":{"type":"x","hidden":true}}]}]}`,
		"bare component":    `{"v":2,"nodes":[{"id":"b","t":"component","p":{"type":"x"}}]}`,
		"nested components": `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section"},"c":[{"id":"n","t":"container","c":[{"id":"b","t":"component","p":{"type":"x"}}]}]}]}`,
	}
	for name, s := range cases {
		d, err := Parse([]byte(s))
		if err != nil {
			t.Errorf("%s: fixture invalid: %v", name, err)
			continue
		}
		if V1Expressible(d) {
			t.Errorf("%s: reported expressible", name)
		}
		if secs, ok := ToSections(d); ok || secs != nil {
			t.Errorf("%s: ToSections ok=%v", name, ok)
		}
	}
	if V1Expressible(nil) {
		t.Error("nil doc expressible")
	}
}

func TestExpressibleAfterOps(t *testing.T) {
	d := FromSections(sampleSections())
	if _, err := d.Apply(Op{Op: "setStyle", ID: "c-s-a", BP: "base", Key: "pt", Value: json.RawMessage(`"space-2"`)}); err != nil {
		t.Fatal(err)
	}
	if V1Expressible(d) {
		t.Fatal("styled section still expressible")
	}
	if _, err := d.Apply(Op{Op: "setStyle", ID: "c-s-a", BP: "base", Key: "pt", Value: json.RawMessage(`null`)}); err != nil {
		t.Fatal(err)
	}
	if !V1Expressible(d) {
		t.Fatal("not expressible after undo")
	}
}

func TestComponentsDocumentOrder(t *testing.T) {
	d, err := Parse([]byte(`{"v":2,"nodes":[
		{"id":"r","t":"container","c":[
			{"id":"k1","t":"component","p":{"type":"x"}},
			{"id":"in","t":"container","c":[{"id":"k2","t":"component","p":{"type":"x"}}]},
			{"id":"h","t":"heading"}]},
		{"id":"k3","t":"component","p":{"type":"x"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range Components(d) {
		ids = append(ids, c.ID)
	}
	if want := []string{"k1", "k2", "k3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("%v", ids)
	}
	if Components(nil) != nil {
		t.Fatal("nil doc")
	}
}

func TestSectionFromMap_TolerantSortOrderAndExtra(t *testing.T) {
	for in, want := range map[string]int{`"2"`: 2, ` " 3 "`: 3, `4`: 4, `"x"`: 0, `null`: 0} {
		secs := SectionsFromJSON([]byte(`{"page_sections":[{"id":"a","sort_order":`+in+`}]}`), true)
		if len(secs) != 1 || secs[0].SortOrder != want {
			t.Errorf("sort_order %s: %+v, want %d", in, secs, want)
		}
	}
}

func TestSections_UnknownKeysRoundTrip(t *testing.T) {
	secs := SectionsFromJSON([]byte(`{"page_sections":[{"id":"a","type":"hero","anchor":"top","meta":{"n":9.90},"content":{}}]}`), true)
	back, ok := ToSections(FromSections(secs))
	if !ok || len(back) != 1 {
		t.Fatalf("ok=%v back=%+v", ok, back)
	}
	m := SectionMaps(back)[0].(map[string]any)
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), `"anchor":"top"`) || !strings.Contains(string(b), `"meta":{"n":9.90}`) {
		t.Errorf("unknown keys lost: %s", b)
	}
}

func TestLegacySections(t *testing.T) {
	doc := FromSections([]Section{{ID: "a", Type: "content", Content: map[string]any{"body": "x"}}})
	ss, ok := LegacySections(doc)
	if !ok || len(ss) != 1 || ss[0].ID != "a" {
		t.Fatalf("ss=%v ok=%v", ss, ok)
	}
	doc.Nodes = append(doc.Nodes, &Node{ID: "r", T: "container", P: map[string]any{"tag": "div"}})
	if _, ok := LegacySections(doc); ok {
		t.Error("builder-only doc has sections")
	}
	if _, ok := LegacySections(nil); ok {
		t.Error("nil doc has sections")
	}
}

func TestNavlabelKeepsV1Shape(t *testing.T) {
	d := FromSections(sampleSections())
	d.Nodes[0].P["navlabel"] = "Renamed"
	d.Nodes[0].C[0].P["navlabel"] = "Inner"
	secs, ok := LegacySections(d)
	if !ok || len(secs) != len(d.Nodes) {
		t.Fatalf("renamed v1 doc lost its sections: ok=%v len=%d", ok, len(secs))
	}
	if _, ok := V1Shaped(d.Nodes[0]); !ok {
		t.Fatal("navlabel broke v1 shape")
	}
	d.Nodes[1].P["navlabel"] = 5.0
	if _, ok := V1Shaped(d.Nodes[1]); ok {
		t.Fatal("non-string navlabel must not be v1-shaped")
	}
}
