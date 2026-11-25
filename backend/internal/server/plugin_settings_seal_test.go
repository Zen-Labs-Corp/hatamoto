package server

import (
	"encoding/json/jsontext"
	"testing"

	"hatamoto.dev/core/internal/packages"
)

func TestPluginSettingsSchemas(t *testing.T) {
	const schema = `{"type":"object","properties":{"api_key":{"type":"string","writeOnly":true}}}`
	rows := []packages.Row{
		{Kind: packages.KindTheme, Name: "a-theme", Manifest: jsontext.Value(`{"name":"a-theme","settings_schema":` + schema + `}`)},
		{Kind: packages.KindPlugin, Name: "no-schema", Manifest: jsontext.Value(`{"name":"no-schema"}`)},
		{Kind: packages.KindPlugin, Name: "broken", Manifest: jsontext.Value(`not json`)},
		{Kind: packages.KindPlugin, Name: "paid-lookup", Manifest: jsontext.Value(`{"name":"paid-lookup","settings_schema":` + schema + `}`)},
	}
	got := pluginSettingsSchemas(rows)
	if len(got) != 1 || len(got["paid-lookup"]) == 0 {
		t.Fatalf("schemas = %v, want only paid-lookup", got)
	}
}
