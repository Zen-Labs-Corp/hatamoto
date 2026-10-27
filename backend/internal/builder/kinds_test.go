package builder

import (
	"reflect"
	"testing"
)

func TestKindsOrderAndCoverage(t *testing.T) {
	var got []string
	for _, k := range Kinds() {
		got = append(got, k.Kind)
	}
	want := []string{"container", "heading", "text", "button", "image", "spacer", "divider", "component", "ref", "slot", "columns", "richtext", "buttons", "quote", "list", "stat", "table", "html", "gallery", "icon", "video", "map", "tabs", "tabpanel", "accordion", "accorditem", "form", "loop", "loopempty", "menu", "breadcrumbs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if len(got) != len(reg.Load().kinds) {
		t.Fatalf("Kinds lists %d of %d registered kinds", len(got), len(reg.Load().kinds))
	}
	if !reflect.DeepEqual(Kinds(), Kinds()) {
		t.Fatal("Kinds not stable")
	}
}

func TestKindsProps(t *testing.T) {
	byKind := map[string]KindInfo{}
	for _, k := range Kinds() {
		byKind[k.Kind] = k
	}
	if !byKind["container"].Children || byKind["heading"].Children {
		t.Error("Children flag wrong")
	}
	h := byKind["heading"].Props
	if len(h) != 4 || h[0].Key != "html" || h[1].Key != "level" || h[2].Key != "text" || h[3].Key != "hidden" {
		t.Fatalf("heading props = %+v", h)
	}
	lv := h[1]
	if lv.Type != "integer" || lv.Min == nil || *lv.Min != 1 || lv.Max == nil || *lv.Max != 6 {
		t.Errorf("level = %+v", lv)
	}
	for _, p := range byKind["container"].Props {
		if p.Key == "tag" && (p.Type != "string" || len(p.Enum) != 8) {
			t.Errorf("tag = %+v", p)
		}
		if p.Key == "hidden" && p.Type != "boolean" {
			t.Errorf("hidden = %+v", p)
		}
	}
	c := byKind["component"].Props
	if c[0].Key != "type" || !c[0].Required {
		t.Errorf("component props = %+v", c)
	}
	// Mutating the result must not leak into the registry.
	byKind["heading"].Default["text"] = "x"
	if Kinds()[1].Default["text"] != "Heading" {
		t.Error("Default shared between calls")
	}
}

func TestNewNodeValidInDoc(t *testing.T) {
	for _, k := range Kinds() {
		if k.Kind == "component" || k.Kind == "ref" {
			if _, err := NewNode(k.Kind, "c1"); err == nil {
				t.Errorf("NewNode(%s) should fail", k.Kind)
			}
			continue
		}
		n, err := NewNode(k.Kind, "n1")
		if err != nil {
			t.Fatalf("%s: %v", k.Kind, err)
		}
		host := &Node{ID: "root", T: "container", P: map[string]any{"tag": "div"}, C: []*Node{n}}
		if k.OnlyIn != "" {
			host = &Node{ID: "root", T: k.OnlyIn, C: []*Node{n}}
		}
		d := &Doc{V: 2, Nodes: []*Node{host}}
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", k.Kind, err)
		}
	}
	h, _ := NewNode("heading", "h")
	if h.P["text"] != "Heading" || h.P["level"] != float64(2) {
		t.Errorf("heading = %+v", h.P)
	}
	if _, err := NewNode("nope", "x"); err == nil {
		t.Error("unknown kind")
	}
	if _, err := NewNode("text", "bad id"); err == nil {
		t.Error("bad id")
	}
	c, err := NewComponent("hero", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Doc{V: 2, Nodes: []*Node{c}}).Validate(); err != nil {
		t.Error(err)
	}
	if _, err := NewComponent("", "c1"); err == nil {
		t.Error("empty component type")
	}
}

func TestCoerceProp(t *testing.T) {
	ok := []struct {
		kind, key, raw string
		want           any
	}{
		{"heading", "level", "3", float64(3)},
		{"heading", "text", "Hi", "Hi"},
		{"heading", "hidden", "on", true},
		{"text", "hidden", "1", true},
		{"text", "hidden", "true", true},
		{"text", "hidden", "false", false},
		{"container", "tag", "section", "section"},
		{"heading", "level", "", nil},
		{"component", "content", `{"a":1}`, map[string]any{"a": float64(1)}},
	}
	for _, c := range ok {
		got, err := CoerceProp(c.kind, c.key, c.raw)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s.%s(%q) = %v, %v; want %v", c.kind, c.key, c.raw, got, err, c.want)
		}
	}
	bad := []struct{ kind, key, raw string }{
		{"heading", "level", "9"},
		{"heading", "level", "x"},
		{"heading", "level", "2.5"},
		{"text", "hidden", "maybe"},
		{"container", "tag", "table"},
		{"component", "content", "[1]"},
		{"nope", "text", "x"},
		{"heading", "nope", "x"},
	}
	for _, c := range bad {
		if v, err := CoerceProp(c.kind, c.key, c.raw); err == nil {
			t.Errorf("%s.%s(%q) = %v, want error", c.kind, c.key, c.raw, v)
		}
	}
}

func TestReuseKindsHidden(t *testing.T) {
	for _, k := range Kinds() {
		if want := k.Kind == "ref" || k.Kind == "slot" || k.Kind == "loopempty"; k.Hidden != want {
			t.Errorf("%s Hidden = %v", k.Kind, k.Hidden)
		}
	}
}

func TestSlotReplaceProp(t *testing.T) {
	ok, err := Parse([]byte(`{"v":2,"nodes":[{"id":"s1","t":"slot","p":{"replace":true}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !SlotReplaces(ok) {
		t.Error("SlotReplaces missed a replace slot")
	}
	plain, _ := Parse([]byte(`{"v":2,"nodes":[{"id":"s1","t":"slot"}]}`))
	if SlotReplaces(plain) || SlotReplaces(nil) {
		t.Error("SlotReplaces true without replace")
	}
	if _, err := Parse([]byte(`{"v":2,"nodes":[{"id":"s1","t":"slot","p":{"replace":"yes"}}]}`)); err == nil {
		t.Error("a non-boolean replace was accepted")
	}
	for _, k := range Kinds() {
		if k.Kind != "slot" {
			continue
		}
		keys := map[string]bool{}
		for _, p := range k.Props {
			keys[p.Key] = true
		}
		if !keys["replace"] || !keys["hidden"] {
			t.Errorf("slot props = %v", keys)
		}
	}
}
