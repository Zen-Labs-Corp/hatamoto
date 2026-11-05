package server

import (
	"strings"
	"testing"

	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
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

// A non-public route has no "public" key; a public one has "public":true. A
// declared empty csp block is still listed.
func TestPluginViewRoutePublicAndEmptyCSP(t *testing.T) {
	b, err := jsonx.Marshal(pluginView{Name: "p", CSP: &sdk.CSP{},
		Routes: []routeView{{Method: "GET", Path: "/a"}, {Method: "GET", Path: "/b", Public: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`{"method":"GET","path":"/a"}`, `{"method":"GET","path":"/b","public":true}`, `"csp":{}`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}
