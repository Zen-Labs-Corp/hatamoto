package packages

import (
	"errors"
	"strings"
	"testing"
)

func TestEntitySchemaShipped(t *testing.T) {
	const withSchema = `{"name":"board","version":"1.0.0","sdk":"^0.27","permissions":["entities.write.own"],"entity_schema":"story"}`
	const without = `{"name":"board","version":"1.0.0","sdk":"^0.27"}`
	build := func(manifest string, extra map[string]string) *Package {
		t.Helper()
		files := map[string][]byte{PluginManifest: []byte(manifest), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}
		for name, body := range extra {
			files[name] = []byte(body)
		}
		raw, err := PackFiles(files, nil)
		if err != nil {
			t.Fatal(err)
		}
		return parse(t, raw)
	}
	check := func(p *Package) error {
		t.Helper()
		man, err := p.PluginManifest()
		if err != nil {
			t.Fatal(err)
		}
		return checkEntitySchemaShipped(p, man)
	}
	schema := map[string]string{"schemas/story.json": `{"title":"Story","type":"object"}`}

	if err := check(build(withSchema, schema)); err != nil {
		t.Errorf("shipped schema refused: %v", err)
	}
	var pe *ParseError
	if err := check(build(withSchema, nil)); !errors.As(err, &pe) || !strings.Contains(err.Error(), "schemas/story.json") {
		t.Errorf("missing schema: %v", err)
	}
	section := map[string]string{"schemas/story.json": schema["schemas/story.json"], "sections/story.html": "<div></div>"}
	if err := check(build(withSchema, section)); !errors.As(err, &pe) {
		t.Errorf("section accepted as a content type: %v", err)
	}
	if err := check(build(without, nil)); err != nil {
		t.Errorf("no entity_schema refused: %v", err)
	}
}
