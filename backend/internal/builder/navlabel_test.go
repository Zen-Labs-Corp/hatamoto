package builder

import (
	"strings"
	"testing"
)

// navlabel is the universal, never-rendered name of a node in the navigator.
func TestNavlabelOnEveryKind(t *testing.T) {
	for name := range reg.Load().kinds {
		build := func(label string) *Doc {
			p := map[string]any{"navlabel": label}
			if name == "component" {
				p["type"] = "hero"
			}
			if name == "ref" {
				p["block"] = "11111111-2222-3333-4444-555555555555"
			}
			n := &Node{ID: "a", T: name, P: p}
			d := &Doc{V: 2, Nodes: []*Node{n}}
			if k, _ := kindOf(name); k.meta != nil && k.meta.OnlyIn != "" {
				d.Nodes = []*Node{{ID: "host", T: k.meta.OnlyIn, C: []*Node{n}}}
			}
			return d
		}
		if err := build(strings.Repeat("é", 80)).Validate(); err != nil {
			t.Errorf("kind %s rejects an 80-rune navlabel: %v", name, err)
		}
		if err := build(strings.Repeat("x", 81)).Validate(); err == nil {
			t.Errorf("kind %s accepts an 81-rune navlabel", name)
		}
		if err := build("two\nlines").Validate(); err == nil {
			t.Errorf("kind %s accepts a control character in navlabel", name)
		}
	}
}

func TestNavlabelHiddenFromKindProps(t *testing.T) {
	for _, k := range Kinds() {
		for _, p := range k.Props {
			if p.Key == "navlabel" {
				t.Errorf("kind %s lists the navlabel prop", k.Kind)
			}
		}
	}
}

func TestNavlabelNotDeclaredByKindSchemas(t *testing.T) {
	for name, s := range reg.Load().schemas {
		if _, ok := s.Properties["navlabel"]; !ok {
			t.Errorf("kind %s has no navlabel", name)
		}
	}
	for _, schema := range []string{`{"type":"object","properties":{"navlabel":{"type":"string"}}}`} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("withUniversal did not panic on a duplicate navlabel")
				}
			}()
			withUniversal("x", schema)
		}()
	}
}

func TestNavlabelNamesDiffRows(t *testing.T) {
	a := doc(&Node{ID: "h", T: "heading", P: map[string]any{"text": "Welcome"}})
	b := doc(&Node{ID: "h", T: "heading", P: map[string]any{"text": "Welcome", "navlabel": "Hero title"}})
	c := DiffDocs(a, b).Changes[0]
	if c.Hint != "Hero title" {
		t.Errorf("hint = %q, want the navlabel", c.Hint)
	}
	if len(c.Props) != 1 || c.Props[0].Key != "navlabel" || c.Props[0].Kind != DiffAdded {
		t.Errorf("props = %+v", c.Props)
	}
}
