package builder

import (
	"strings"
	"testing"
)

func inlineDoc(kind string, p map[string]any, b map[string]string) *Doc {
	return &Doc{V: 2, Nodes: []*Node{{ID: "n1", T: kind, P: p, B: b}}}
}

// The html prop of heading and text: a plain string up to 20000 characters,
// never bindable, and absent from the defaults (fresh nodes stay plain).
func TestInlineHTMLProp(t *testing.T) {
	for _, kind := range []string{"heading", "text"} {
		if err := inlineDoc(kind, map[string]any{"html": "<b>x</b>"}, nil).Validate(); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
		if err := inlineDoc(kind, map[string]any{"html": strings.Repeat("a", 20000)}, nil).Validate(); err != nil {
			t.Errorf("%s at the cap: %v", kind, err)
		}
		if err := inlineDoc(kind, map[string]any{"html": strings.Repeat("a", 20001)}, nil).Validate(); err == nil {
			t.Errorf("%s: html over 20000 accepted", kind)
		}
		if err := inlineDoc(kind, map[string]any{"text": "x"}, map[string]string{"html": "entity.name"}).Validate(); err == nil {
			t.Errorf("%s: binding html accepted", kind)
		}
		for _, k := range Kinds() {
			if k.Kind != kind {
				continue
			}
			if _, ok := k.Default["html"]; ok {
				t.Errorf("%s: html in the defaults", kind)
			}
			for _, p := range k.Props {
				if p.Key == "html" && (p.Bind != "" || p.Widget != "") {
					t.Errorf("%s: html = %+v", kind, p)
				}
			}
		}
	}
}
