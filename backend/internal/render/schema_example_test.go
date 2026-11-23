package render

import (
	"strings"
	"testing"
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
