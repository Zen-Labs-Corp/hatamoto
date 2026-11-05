package app

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// Install reads a manifest case-insensitively; a stored one must read the same.
func TestStoredManifestKeysMatchCaseInsensitively(t *testing.T) {
	var man sdk.Manifest
	raw := []byte(`{"Name":"acme","Settings_Schema":{"type":"object"}}`)
	if err := jsonx.Unmarshal(raw, &man, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if man.Name != "acme" || len(man.SettingsSchema) == 0 {
		t.Fatalf("miscased keys lost: %+v", man)
	}
}
