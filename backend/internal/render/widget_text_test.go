package render

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// The icon enum of both schemas lists exactly the names of icons.json.
func TestIconEnumMatchesGlyphs(t *testing.T) {
	var keys []string
	for k := range icons() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, kind := range []string{"icon", "list"} {
		raw, err := os.ReadFile("../builder/widgets/" + kind + "/schema.json")
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		key := "name"
		if kind == "list" {
			key = "icon"
		}
		got := append([]string(nil), s.Properties[key].Enum...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, keys) {
			t.Errorf("%s enum = %v, icons.json = %v", kind, got, keys)
		}
	}
}

func TestTSVRows(t *testing.T) {
	got := tsvRows("a\tb\tc\r\n\n\tonly\nx")
	want := [][]string{{"a", "b", "c"}, {"", "only", ""}, {"x", "", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
	big := strings.Repeat("1\t2\n", 300)
	if r := tsvRows(big); len(r) != maxTableRows {
		t.Errorf("rows = %d", len(r))
	}
	if r := tsvRows(strings.Repeat("c\t", 50)); len(r[0]) != maxTableCols {
		t.Errorf("cols = %d", len(r[0]))
	}
}

// Rich text and html props are sanitised at render time, whatever is stored.
func TestTextWidgetsSanitise(t *testing.T) {
	vectors := []string{
		`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `<a href="javascript:alert(1)">x</a>`,
		`<p onclick="x()">c</p>`, `<iframe src="https://evil.example/x"></iframe>`,
	}
	for _, kind := range []string{"richtext", "html"} {
		for _, v := range vectors {
			d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "n1", T: kind, P: map[string]any{"html": "<p>ok</p>" + v}}}}
			out := mainInner(t, renderDoc(t, nil, d, false))
			for _, bad := range []string{"<script", "onerror", "onclick", "javascript:", "<iframe", "evil.example"} {
				if strings.Contains(out, bad) {
					t.Errorf("%s %q: output keeps %q: %s", kind, v, bad, out)
				}
			}
			if !strings.Contains(out, "<p>ok</p>") {
				t.Errorf("%s: lost the safe markup: %s", kind, out)
			}
		}
	}
}
