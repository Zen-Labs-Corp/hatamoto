package builder

import (
	"strings"
	"testing"
	"testing/fstest"
)

const tabsSchema = `{
	"type":"object","additionalProperties":false,
	"x-hm":{"label":"Tabs","hint":"Tabbed panels","group":"Interactive","order":2,"children":true,
		"seed":[{"t":"container"},{"t":"text","p":{"text":"Tab"}}]},
	"properties":{
		"zeta":{"type":"string","maxLength":40,"x-hm-widget":"textarea"},
		"alpha":{"type":"integer","minimum":1,"maximum":3},
		"hidden":{"type":"boolean"}}}`

func widgetFS1(schema string) fstest.MapFS {
	return fstest.MapFS{"widgets/probe/schema.json": {Data: []byte(schema)}, "widgets/README.md": {Data: []byte("x")}}
}

// withKind registers the loaded widgets for one test, like the init merge does.
func withKind(t *testing.T, schema string) {
	t.Helper()
	ws, err := loadWidgets(widgetFS1(schema), nativeKinds)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]kindSpec{}
	for k, v := range nativeKinds {
		all[k] = v
	}
	for k, v := range ws {
		all[k] = v
	}
	old := reg.Swap(newRegistry(all))
	t.Cleanup(func() { reg.Store(old) })
}

func TestLoadWidgetsAcceptsValid(t *testing.T) {
	ws, err := loadWidgets(widgetFS1(tabsSchema), nativeKinds)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ws["probe"]
	if !ok || !s.children || s.meta.Label != "Tabs" {
		t.Fatalf("spec = %+v", s)
	}
	if got := strings.Join(s.order, ","); got != "zeta,alpha,hidden" {
		t.Errorf("order = %s", got)
	}
	if s.widgets["zeta"] != "textarea" {
		t.Errorf("widgets = %v", s.widgets)
	}
}

func TestLoadWidgetsRejects(t *testing.T) {
	cases := map[string]string{
		"array prop":        `{"x-hm":{"label":"A","group":"Content"},"properties":{"items":{"type":"array"}}}`,
		"array union":       `{"x-hm":{"label":"A","group":"Content"},"properties":{"items":{"type":["null","array"]}}}`,
		"bad group":         `{"x-hm":{"label":"A","group":"Nope"},"properties":{}}`,
		"no x-hm":           `{"properties":{}}`,
		"no label":          `{"x-hm":{"group":"Content"}}`,
		"seed w/o children": `{"x-hm":{"label":"A","group":"Content","seed":[{"t":"text"}]}}`,
		"unknown seed kind": `{"x-hm":{"label":"A","group":"Content","children":true,"seed":[{"t":"nope"}]}}`,
		"bad json":          `{`,
	}
	for name, schema := range cases {
		if _, err := loadWidgets(widgetFS1(schema), nativeKinds); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	collide := mapWith("text", `{"x-hm":{"label":"A","group":"Content"}}`)
	if _, err := loadWidgets(collide, nativeKinds); err == nil {
		t.Error("native collision accepted")
	}
}

func mapWith(kind, schema string) fstest.MapFS {
	return fstest.MapFS{"widgets/" + kind + "/schema.json": {Data: []byte(schema)}}
}

func TestEmbeddedWidgetsLoad(t *testing.T) {
	// Whatever ships must have merged cleanly at init (no array props, valid meta).
	for name, k := range reg.Load().kinds {
		if k.meta == nil {
			continue
		}
		if schemaOf(name) == nil {
			t.Errorf("%s: no compiled schema", name)
		}
	}
}

func TestWidgetKindInfoAndNewTree(t *testing.T) {
	withKind(t, tabsSchema)
	var ki KindInfo
	for _, k := range Kinds() {
		if k.Kind == "probe" {
			ki = k
		}
	}
	if ki.Kind == "" || ki.Label != "Tabs" || ki.Group != "Interactive" || !ki.Children || len(ki.Seed) != 2 {
		t.Fatalf("KindInfo = %+v", ki)
	}
	if got := ki.Props[0].Key + "," + ki.Props[1].Key + "," + ki.Props[2].Key; got != "zeta,alpha,hidden" {
		t.Errorf("prop order = %s", got)
	}
	if p := ki.Props[0]; p.Widget != "textarea" || p.MaxLength != 40 {
		t.Errorf("zeta = %+v", p)
	}
	if p := ki.Props[1]; p.Widget != "" || p.MaxLength != 0 || p.Max == nil {
		t.Errorf("alpha = %+v", p)
	}
	// Seed fresh copy
	ki.Seed[1].P["text"] = "x"
	for _, k := range Kinds() {
		if k.Kind == "probe" && k.Seed[1].P["text"] != "Tab" {
			t.Error("Seed shared")
		}
	}

	n, err := NewTree("probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.C) != 2 || n.C[0].T != "container" || n.C[1].P["text"] != "Tab" {
		t.Fatalf("tree = %+v", n)
	}
	n2, _ := NewTree("probe")
	if n.ID == n2.ID || n.C[0].ID == n2.C[0].ID || n.C[0].ID == n.C[1].ID {
		t.Error("ids not fresh")
	}
	if err := (&Doc{V: 2, Nodes: []*Node{&n}}).Validate(); err != nil {
		t.Error(err)
	}
	// Schema rejects an unknown prop.
	n.P = map[string]any{"nope": 1}
	if err := (&Doc{V: 2, Nodes: []*Node{&n}}).Validate(); err == nil {
		t.Error("unknown prop accepted")
	}
	if _, err := NewTree("nope"); err == nil {
		t.Error("unknown kind")
	}
	if _, err := NewTree("component"); err == nil {
		t.Error("component tree")
	}
	if _, err := NewTree("text"); err != nil {
		t.Errorf("native tree: %v", err)
	}
}

func TestKindsOrderWidgetsAfterNatives(t *testing.T) {
	withKind(t, tabsSchema)
	ks := Kinds()
	if last := ks[len(ks)-1].Kind; last != "probe" {
		t.Errorf("last = %s", last)
	}
}
