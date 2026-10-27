package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const promoSchema = `{
	"type":"object","additionalProperties":false,
	"x-hm":{"label":"Promo banner","hint":"A banner","group":"Layout","children":true},
	"properties":{
		"title":{"type":"string","maxLength":80,"x-hm-bind":"text"},
		"link":{"type":"string","x-hm-bind":"url"}}}`

func packOf(t *testing.T, plugin, widget, schema string, own ...string) PackWidgetSchema {
	t.Helper()
	o := map[string]bool{}
	for _, k := range own {
		o[k] = true
	}
	w, err := ParsePackWidget(plugin, widget, []byte(schema), o)
	if err != nil {
		t.Fatalf("ParsePackWidget(%s,%s): %v", plugin, widget, err)
	}
	return w
}

func withPack(t *testing.T, ws ...PackWidgetSchema) {
	t.Helper()
	if err := SetPackKinds(ws); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPackKinds(nil) })
}

func TestPackKindNameRules(t *testing.T) {
	long := strings.Repeat("a", 41)
	for _, c := range []struct {
		plugin, widget string
		ok             bool
	}{
		{"promo", "banner", true},
		{"acme-seo", "card", true},
		{"promo", "a", true},
		{"stat", "", false},
		{"promo", "ban_ner", false},
		{"pro_mo", "banner", false},
		{"Promo", "banner", false},
		{"promo", "Banner", false},
		{"promo", "_x", false},
		{"promo", "-x", false},
		{"a", "x", false}, // plugin names have at least 2 chars
		{long, "x", false},
		{strings.Repeat("p", 40), strings.Repeat("w", 32), false}, // 73 > ValidID limit
		{strings.Repeat("p", 30), strings.Repeat("w", 32), true},
	} {
		got, err := PackKindName(c.plugin, c.widget)
		if (err == nil) != c.ok {
			t.Errorf("PackKindName(%q,%q) = %q, %v; want ok=%v", c.plugin, c.widget, got, err, c.ok)
			continue
		}
		if c.ok {
			if !IsPackKind(got) || !ValidID(got) || strings.Count(got, "_") != 1 {
				t.Errorf("kind %q is not a well-formed pack kind", got)
			}
		}
	}
	for _, n := range []string{"stat", "a_b_c", "Promo_x", "promo__x", "promo_", "_x", "heading"} {
		if IsPackKind(n) {
			t.Errorf("IsPackKind(%q) = true", n)
		}
	}
	if !IsPackKind("promo_banner") || !IsPackKind("acme-seo_card") {
		t.Error("valid pack kinds rejected")
	}
}

func FuzzPackKindName(f *testing.F) {
	f.Add("promo", "banner")
	f.Add("a_b", "c_d")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, plugin, widget string) {
		k, err := PackKindName(plugin, widget)
		if err != nil {
			return
		}
		if !IsPackKind(k) || !ValidID(k) || strings.Count(k, "_") != 1 {
			t.Fatalf("kind %q from (%q,%q) is malformed", k, plugin, widget)
		}
		if _, core := coreRegistry.kinds[k]; core {
			t.Fatalf("kind %q shadows a core kind", k)
		}
	})
}

func TestPackWidgetValidatesAndOrphans(t *testing.T) {
	w := packOf(t, "promo", "banner", promoSchema)
	doc := func() *Doc {
		return &Doc{V: 2, Nodes: []*Node{{ID: "a", T: "promo_banner", P: map[string]any{"title": "Hi"},
			B: map[string]string{"link": "entity.url"},
			C: []*Node{{ID: "b", T: "text", P: map[string]any{"text": "x"}}}}}}
	}
	if err := doc().Validate(); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		// orphan-shaped names are tolerated, so a bad registered doc is the check below
		if err != nil {
			t.Fatalf("orphan before registration should validate: %v", err)
		}
	}
	withPack(t, w)
	if err := doc().Validate(); err != nil {
		t.Fatalf("registered: %v", err)
	}
	bad := doc()
	bad.Nodes[0].P["title"] = 5
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("schema violation accepted: %v", err)
	}
	bad = doc()
	bad.Nodes[0].P["nope"] = "x"
	if err := bad.Validate(); err == nil {
		t.Fatal("additional prop accepted")
	}
	if _, err := NewTree("promo_banner"); err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	if _, err := CoerceProp("promo_banner", "title", "x"); err != nil {
		t.Fatal(err)
	}

	// Unregister: the node becomes an orphan.
	if err := SetPackKinds(nil); err != nil {
		t.Fatal(err)
	}
	d := doc()
	if err := d.Validate(); err != nil {
		t.Fatalf("orphan rejected: %v", err)
	}
	for _, op := range []Op{
		{Op: "setProp", ID: "a", Key: "title", Value: json.RawMessage(`"New"`)},
		{Op: "setBind", ID: "a", Key: "title", Value: json.RawMessage(`"entity.title"`)},
	} {
		if _, err := d.Apply(op); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "widget not installed") {
			t.Fatalf("%s on orphan: %v", op.Op, err)
		}
	}
	if _, err := d.Apply(Op{Op: "setProp", ID: "b", Key: "text", Value: json.RawMessage(`"y"`)}); err != nil {
		t.Fatalf("child of orphan editable: %v", err)
	}
	if _, err := d.Apply(Op{Op: "setStyle", ID: "a", BP: "base", Key: "pt", Value: json.RawMessage(`"space-2"`)}); err != nil {
		t.Fatalf("style of orphan: %v", err)
	}
	if _, err := d.Apply(Op{Op: "move", ID: "b", Parent: "", Index: 0}); err != nil {
		t.Fatalf("move child out: %v", err)
	}
	if _, err := d.Apply(Op{Op: "remove", ID: "a"}); err != nil {
		t.Fatalf("delete orphan: %v", err)
	}
	if _, err := NewNode("promo_banner", "z"); err == nil {
		t.Fatal("NewNode of an unregistered pack kind must fail")
	}
	// Re-register: props intact.
	d = doc()
	withPack(t, w)
	if err := d.Validate(); err != nil || d.Nodes[0].P["title"] != "Hi" {
		t.Fatalf("restored: %v %v", err, d.Nodes[0].P)
	}
}

func TestOrphanLimitsAndTypos(t *testing.T) {
	for _, c := range []struct {
		name string
		n    *Node
		want string
	}{
		{"typo", &Node{ID: "a", T: "foo"}, "unknown kind"},
		{"core-like", &Node{ID: "a", T: "stat2"}, "unknown kind"},
		{"big props", &Node{ID: "a", T: "gone_widget", P: map[string]any{"x": strings.Repeat("a", 9000)}}, "larger"},
		{"bad bind key", &Node{ID: "a", T: "gone_widget", B: map[string]string{"a b": "entity.title"}}, "binding key"},
		{"bad bind src", &Node{ID: "a", T: "gone_widget", B: map[string]string{"x": "nope.x"}}, "unknown binding"},
	} {
		err := (&Doc{V: 2, Nodes: []*Node{c.n}}).Validate()
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	ok := &Doc{V: 2, Nodes: []*Node{{ID: "a", T: "gone_widget", C: []*Node{{ID: "b", T: "text", P: map[string]any{"text": "t"}}}}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// children of an orphan are still validated
	ok.Nodes[0].C[0].T = "foo"
	if err := ok.Validate(); err == nil {
		t.Fatal("bad child of an orphan accepted")
	}
}

func TestParsePackWidgetRules(t *testing.T) {
	many := func(n int) string {
		var ps []string
		for i := 0; i < n; i++ {
			ps = append(ps, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
		}
		return `{"type":"object","additionalProperties":false,"x-hm":{"label":"L"},"properties":{` + strings.Join(ps, ",") + `}}`
	}
	cases := []struct{ name, schema, want string }{
		{"no label", `{"type":"object","additionalProperties":false,"x-hm":{},"properties":{}}`, "label"},
		{"no x-hm", `{"type":"object","additionalProperties":false,"properties":{}}`, "x-hm"},
		{"array prop", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L"},"properties":{"a":{"type":"array"}}}`, "array"},
		{"no additionalProperties", `{"type":"object","x-hm":{"label":"L"},"properties":{}}`, "additionalProperties"},
		{"additionalProperties true", `{"type":"object","additionalProperties":true,"x-hm":{"label":"L"},"properties":{}}`, "additionalProperties"},
		{"bad bind", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L"},"properties":{"a":{"type":"string","x-hm-bind":"html"}}}`, "x-hm-bind"},
		{"foreign seed", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L","children":true,"seed":[{"t":"other_card"}]},"properties":{}}`, "seed"},
		{"foreign only_in", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L","only_in":"other_card"},"properties":{}}`, "only_in"},
		{"25 props", many(25), "more than 24"},
		{"too big", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L","hint":"` + strings.Repeat("x", 17000) + `"},"properties":{}}`, "larger"},
		{"not json", `{`, "schema"},
		{"bad schema", `{"type":"nope","additionalProperties":false,"x-hm":{"label":"L"},"properties":{}}`, "schema"},
		{"external ref", `{"type":"object","additionalProperties":false,"x-hm":{"label":"L"},"properties":{"a":{"$ref":"file:///etc/passwd"}}}`, "schema"},
	}
	for _, c := range cases {
		_, err := ParsePackWidget("promo", "w", []byte(c.schema), nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := ParsePackWidget("promo", "w", []byte(many(24)), nil); err != nil {
		t.Errorf("24 props: %v", err)
	}
	// own / native / core seeds are fine; the group is forced.
	s := `{"type":"object","additionalProperties":false,"x-hm":{"label":"L","group":"Data","children":true,
		"seed":[{"t":"promo_item"},{"t":"text"},{"t":"stat"}]},"properties":{}}`
	w, err := ParsePackWidget("promo", "w", []byte(s), map[string]bool{"promo_item": true})
	if err != nil {
		t.Fatal(err)
	}
	if w.spec.meta.Group != PackGroup {
		t.Errorf("group = %q", w.spec.meta.Group)
	}
	if _, err := ParsePackWidget("promo", "bad_name", []byte(promoSchema), nil); err == nil {
		t.Error("underscore widget name accepted")
	}
}

func TestSetPackKindsRules(t *testing.T) {
	a := packOf(t, "promo", "banner", promoSchema)
	if err := SetPackKinds([]PackWidgetSchema{a, a}); err == nil {
		t.Error("duplicate accepted")
	}
	if err := SetPackKinds([]PackWidgetSchema{{Name: "stat"}}); err == nil {
		t.Error("core name accepted")
	}
	if err := SetPackKinds([]PackWidgetSchema{{Name: "promo_x"}}); err == nil {
		t.Error("unparsed widget accepted")
	}
	many := make([]PackWidgetSchema, 65)
	if err := SetPackKinds(many); err == nil {
		t.Error("65 widgets accepted")
	}
	// A failed call leaves the old snapshot.
	withPack(t, a)
	_ = SetPackKinds([]PackWidgetSchema{a, a})
	if _, ok := kindOf("promo_banner"); !ok {
		t.Error("failed SetPackKinds dropped the previous snapshot")
	}
	core := len(coreRegistry.kinds)
	if len(reg.Load().kinds) != core+1 {
		t.Errorf("kinds = %d, want %d", len(reg.Load().kinds), core+1)
	}
	for k, v := range coreRegistry.kinds {
		if got, _ := kindOf(k); got.schema != v.schema {
			t.Errorf("core kind %s changed", k)
		}
	}
}

func TestKindsListsPackAfterCore(t *testing.T) {
	before := Kinds()
	withPack(t, packOf(t, "zeta", "box", promoSchema), packOf(t, "promo", "banner", promoSchema))
	got := Kinds()
	n := len(before)
	if len(got) != n+2 {
		t.Fatalf("len = %d, want %d", len(got), n+2)
	}
	for i := range before {
		if got[i].Kind != before[i].Kind {
			t.Fatalf("core order changed at %d: %s vs %s", i, got[i].Kind, before[i].Kind)
		}
	}
	if got[n].Kind != "promo_banner" || got[n+1].Kind != "zeta_box" {
		t.Errorf("pack order = %s, %s", got[n].Kind, got[n+1].Kind)
	}
	for _, k := range got[n:] {
		if k.Group != PackGroup || k.Label == "" || len(k.Props) != 2 || !k.Children {
			t.Errorf("%s: %+v", k.Kind, k)
		}
	}
	if got[n].Props[0].Bind != "text" {
		t.Errorf("bind = %q", got[n].Props[0].Bind)
	}
}
