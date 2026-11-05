package server

import (
	"strings"
	"testing"

	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/jsonx"
)

// A manifest that declares settings_schema {} still reports it; only a plugin
// without one omits the key.
func TestPluginViewKeepsEmptySettingsSchema(t *testing.T) {
	b, err := jsonx.Marshal(pluginView{Name: "p", SettingsSchema: jsontext.Value(`{}`)})
	if err != nil || !strings.Contains(string(b), `"settings_schema":{}`) {
		t.Fatalf("%s %v", b, err)
	}
	b, _ = jsonx.Marshal(pluginView{Name: "p"})
	if strings.Contains(string(b), "settings_schema") {
		t.Fatalf("absent schema written: %s", b)
	}
}
