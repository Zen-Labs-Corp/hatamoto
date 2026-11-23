package render

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
)

const exampleSchemaHead = `{
  "x-hm-section": {"title": "T", "example": `

const exampleSchemaTail = `},
  "properties": {
    "headline": {"type": "string", "x-hm-alias": ["title"]},
    "count": {"type": "number"},
    "rank": {"type": "integer"},
    "on": {"type": "boolean"},
    "layout": {"type": "string", "enum": ["split", "stack"]},
    "lines": {"type": "array", "items": {"type": "string"}},
    "ctas": {"type": "array", "items": {"type": "object", "properties": {"label": {"type": "string"}, "url": {"type": "string"}}}},
    "box": {"type": "object", "properties": {"note": {"type": "string"}}},
    "any": {}
  }
}`

func exampleSchema(example string) []byte {
	return []byte(exampleSchemaHead + example + exampleSchemaTail)
}

func TestSectionExampleValid(t *testing.T) {
	ex := `{"headline":"H","count":1.5,"rank":2,"on":true,"layout":"split","lines":["a","b"],
		"ctas":[{"label":"L","url":"#"}],"box":{"note":"n"},"any":[1,{"x":null}]}`
	meta, _, _, err := parseSectionSchema(exampleSchema(ex))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Example) == 0 {
		t.Fatal("Meta.Example is empty")
	}
}

func TestSectionExampleAbsent(t *testing.T) {
	meta, _, _, err := parseSectionSchema([]byte(`{"x-hm-section":{"title":"T"},"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Example != nil {
		t.Fatalf("Example = %s, want nil", meta.Example)
	}
}

func TestSectionExampleInvalid(t *testing.T) {
	for _, tc := range []struct{ name, example, want string }{
		{"not an object", `["a"]`, "x-hm-section.example: must be an object"},
		{"unknown key", `{"nope":"x"}`, "x-hm-section.example.nope"},
		{"alias name", `{"title":"x"}`, "x-hm-section.example.title"},
		{"string for number", `{"count":"1"}`, "x-hm-section.example.count: expected number"},
		{"fractional integer", `{"rank":1.5}`, "x-hm-section.example.rank: expected integer"},
		{"enum miss", `{"layout":"grid"}`, "x-hm-section.example.layout"},
		{"null", `{"headline":null}`, "x-hm-section.example.headline: null"},
		{"unknown item key", `{"ctas":[{"label":"a"},{"nope":"b"}]}`, "x-hm-section.example.ctas[1].nope"},
		{"wrong item type", `{"lines":["a",2]}`, "x-hm-section.example.lines[1]: expected string"},
		{"duplicate top-level key", `{"headline":"a","headline":"b"}`, "duplicate"},
		{"duplicate item key", `{"ctas":[{"label":"a"},{"label":"b","label":"c"}]}`, "/ctas/1"},
		{"wrong nested type", `{"box":{"note":3}}`, "x-hm-section.example.box.note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseSectionSchema(exampleSchema(tc.example))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// exampleOf returns the decoded x-hm-section.example of a default-theme section schema.
func exampleOf(t *testing.T, typ string, schema []byte) map[string]any {
	t.Helper()
	var root struct {
		Section struct {
			Example map[string]any `json:"example"`
		} `json:"x-hm-section"`
	}
	if err := jsonx.Unmarshal(schema, &root); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return root.Section.Example
}

func TestDefaultThemeSectionsHaveExamples(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	info := e.ThemeInfo()
	if len(info.Sections) != 25 {
		t.Fatalf("default theme has %d sections, want 25", len(info.Sections))
	}
	for _, s := range info.Sections {
		if len(exampleOf(t, s.Type, s.Schema)) == 0 {
			t.Errorf("section %q has no x-hm-section.example", s.Type)
		}
	}
}

func TestHeroExampleRenders(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	for _, s := range e.ThemeInfo().Sections {
		if s.Type == "hero" {
			content = exampleOf(t, s.Type, s.Schema)
		}
	}
	if len(content) == 0 {
		t.Fatal("hero has no example")
	}
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "h", T: "component", P: map[string]any{"type": "hero", "content": content}}}}
	out := renderDoc(t, nil, d, true)
	if !strings.Contains(out, "A clear headline goes here") {
		t.Error("the hero example headline is not rendered")
	}
	if n := strings.Count(out, `class="hm-btn`); n != 2 {
		t.Errorf("%d buttons rendered, want 2", n)
	}
	if strings.Contains(out, "data-hm-empty") {
		t.Error("the hero example renders the empty placeholder")
	}
}
