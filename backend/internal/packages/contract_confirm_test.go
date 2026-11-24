package packages

import (
	"testing"

	"hatamoto.dev/sdk"
)

func TestContractConfirmFlags(t *testing.T) {
	update := func(perm sdk.Permission) *Package {
		t.Helper()
		// entity_schema needs entities.write.own, and comments.moderate needs entity_schema.
		manifest := `{"name":"board","version":"2.0.0","sdk":"^0.27","permissions":["` + perm + `"]}`
		if perm == sdk.PermCommentsModerate {
			manifest = `{"name":"board","version":"2.0.0","sdk":"^0.27","permissions":["entities.write.own","` + perm + `"],"entity_schema":"story"}`
		} else if perm == sdk.PermEntitiesWriteOwn {
			manifest = `{"name":"board","version":"2.0.0","sdk":"^0.27","permissions":["` + perm + `"],"entity_schema":"story"}`
		}
		raw, err := PackFiles(map[string][]byte{
			PluginManifest:       []byte(manifest),
			PluginWasm:           []byte("\x00asm\x01\x00\x00\x00"),
			"schemas/story.json": []byte(`{"title":"Story","type":"object"}`),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return parse(t, raw)
	}
	for _, perm := range []sdk.Permission{sdk.PermClientKey, sdk.PermEntitiesWriteOwn, sdk.PermCommentsModerate} {
		p := update(perm)
		if !newlyAsks([]byte(`{"name":"board","version":"1.0.0","sdk":"^0.27"}`), p, perm) {
			t.Errorf("%s: a newly asked permission is not flagged", perm)
		}
		if newlyAsks([]byte(`{"name":"board","version":"1.0.0","sdk":"^0.27","permissions":["`+string(perm)+`"]}`), p, perm) {
			t.Errorf("%s: an unchanged permission is flagged", perm)
		}
		if newlyAsks([]byte(`{"name":"board","version":"1.0.0","sdk":"^0.27"}`), update(sdk.PermKV), perm) {
			t.Errorf("%s: an unrelated permission is flagged", perm)
		}
	}
}
