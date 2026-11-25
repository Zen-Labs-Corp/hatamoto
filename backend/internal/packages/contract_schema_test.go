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

func TestStandingLinkFieldsShipped(t *testing.T) {
	manifestFor := func(key string) string {
		return `{"name":"board","version":"1.0.0","sdk":"^0.28",
		"cron":[{"name":"daily","every":"24h"}],
		"runs":[{"name":"import","title":"Import","cron":"daily","stages":["fetch","transform"],
			"standing_approval":{"fields":["price"],"schemas":["event"],"create":{"schemas":["event"],"max_per_hour":5,"link_fields":["` + key + `"]}}}]}`
	}
	check := func(schema string, key ...string) error {
		t.Helper()
		k := append(key, "ticket_url")[0]
		files := map[string][]byte{PluginManifest: []byte(manifestFor(k)), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}
		if schema != "" {
			files["schemas/event.json"] = []byte(schema)
		}
		raw, err := PackFiles(files, nil)
		if err != nil {
			t.Fatal(err)
		}
		p := parse(t, raw)
		man, err := p.PluginManifest()
		if err != nil {
			t.Fatal(err)
		}
		return checkStandingLinkFields(p, man)
	}
	prop := func(def string) string {
		return `{"title":"Event","type":"object","properties":{"ticket_url":` + def + `}}`
	}
	if err := check(prop(`{"type":"string","format":"uri","title":"Ticket link"}`)); err != nil {
		t.Errorf("plain uri field refused: %v", err)
	}
	if err := check(""); err != nil {
		t.Errorf("schema not shipped: %v", err)
	}
	var pe *ParseError
	if err := check(`{"title":"Event","type":"object","properties":{"image_url":{"type":"string","format":"uri"}}}`, "image_url"); !errors.As(err, &pe) || !strings.Contains(err.Error(), "link_fields") {
		t.Errorf("image-named key: %v", err)
	}
	for name, schema := range map[string]string{
		"missing key":   `{"title":"Event","type":"object","properties":{"other":{"type":"string","format":"uri"}}}`,
		"no properties": `{"title":"Event","type":"object"}`,
		"number":        prop(`{"type":"number","format":"uri"}`),
		"no format":     prop(`{"type":"string"}`),
		"image field":   prop(`{"type":"string","format":"uri","x-hm-field":"image"}`),
		"media":         prop(`{"type":"string","format":"uri","x-media":true}`),
		"nested only":   `{"title":"Event","type":"object","properties":{"banner":{"type":"object","properties":{"ticket_url":{"type":"string","format":"uri"}}}}}`,
	} {
		if err := check(schema); !errors.As(err, &pe) || !strings.Contains(err.Error(), "link_fields") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
