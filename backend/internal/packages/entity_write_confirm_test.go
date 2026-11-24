package packages

import (
	"slices"
	"testing"
)

// entityWritePkg is a board plugin update that writes schema with fields.
func entityWritePkg(t *testing.T, version, schema, fields string) *Package {
	t.Helper()
	manifest := `{"name":"board","version":"` + version + `","sdk":"^0.27"`
	if schema != "" {
		manifest += `,"permissions":["entities.write.own"],"entity_schema":"` + schema + `","entity_fields":[` + fields + `]`
	}
	manifest += `}`
	raw, err := PackFiles(map[string][]byte{
		PluginManifest:       []byte(manifest),
		PluginWasm:           []byte("\x00asm\x01\x00\x00\x00"),
		"schemas/story.json": []byte(`{"title":"Story","type":"object"}`),
		"schemas/note.json":  []byte(`{"title":"Note","type":"object"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, raw)
}

func entityWriteManifest(schema, fields string) []byte {
	m := `{"name":"board","version":"1.0.0","sdk":"^0.27"`
	if schema != "" {
		m += `,"permissions":["entities.write.own"],"entity_schema":"` + schema + `","entity_fields":[` + fields + `]`
	}
	return []byte(m + `}`)
}

func TestEntityWriteChange(t *testing.T) {
	cases := []struct {
		name       string
		installed  []byte
		next       *Package
		oldS, newS string
		changed    bool
		newFields  []string
	}{
		{"schema changes", entityWriteManifest("story", `"url"`), entityWritePkg(t, "2.0.0", "note", `"url"`), "story", "note", true, nil},
		{"fields widen", entityWriteManifest("story", `"url"`), entityWritePkg(t, "2.0.0", "story", `"url","text"`), "story", "story", false, []string{"text"}},
		{"fields narrow", entityWriteManifest("story", `"url","text"`), entityWritePkg(t, "2.0.0", "story", `"url"`), "story", "story", false, nil},
		{"permission dropped", entityWriteManifest("story", `"url"`), entityWritePkg(t, "2.0.0", "", ""), "story", "", false, nil},
		{"newly asked", entityWriteManifest("", ""), entityWritePkg(t, "2.0.0", "story", `"url"`), "", "story", false, nil},
		{"unreadable installed", []byte("\x00"), entityWritePkg(t, "2.0.0", "story", `"url"`), "", "story", false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, n, changed, fields := entityWriteChange(c.installed, c.next)
			if o != c.oldS || n != c.newS || changed != c.changed || !slices.Equal(fields, c.newFields) {
				t.Fatalf("got (%q, %q, %v, %v), want (%q, %q, %v, %v)", o, n, changed, fields, c.oldS, c.newS, c.changed, c.newFields)
			}
		})
	}
}
