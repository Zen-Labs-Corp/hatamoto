//go:build integration

package pluginhost

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// Run with:
//
//	DATABASE_URL=postgres://... go test -tags=integration ./internal/pluginhost/...
//
// Expects core migration 006 to be applied; the test creates it if absent.
func TestSettingsStoreRoundTrip(t *testing.T) {
	ctx, pool := settingsTestPool(t)

	const name = "it-settings-plugin"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_plugin_settings WHERE plugin=$1`, name) })
	store := NewSettingsStore(pool, SettingsKey(strings.Repeat("k", 32)), nil)

	got, err := store.Get(ctx, name)
	if err != nil || string(got) != `{}` {
		t.Fatalf("absent: %s, %v", got, err)
	}
	if err := store.Put(ctx, name, []byte(testSchema), jsontext.Value(`{"retries":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, name, []byte(testSchema), jsontext.Value(`{"retries":8}`)); err != nil { // upsert
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Greeting string `json:"greeting"`
		Retries  int    `json:"retries"`
	}
	if err := jsonx.UnmarshalPinned(MergeDefaults([]byte(testSchema), stored), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Retries != 8 || cfg.Greeting != "hi" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func keyFor(c string) []byte { return SettingsKey(strings.Repeat(c, 32)) }

// settingsGetJSON is what a plugin receives from settings.get.
func settingsGetJSON(t *testing.T, ctx context.Context, store *SettingsStore, name string) map[string]jsontext.Value {
	t.Helper()
	e := &callEnv{plugin: name, manifest: sdk.Manifest{SettingsSchema: []byte(testSchema)}}
	got, err := fnSettingsGet(ctx, &API{svc: Services{Settings: store}}, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]jsontext.Value{}
	if err := jsonx.Unmarshal(got.(jsontext.Value), &out, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSettingsStoreSealsWriteOnlyAtRest(t *testing.T) {
	ctx, pool := settingsTestPool(t)
	const name = "it-sealed-at-rest"
	seed(t, ctx, pool, name, `{}`)
	store := NewSettingsStore(pool, keyFor("k"), nil)

	if err := store.Put(ctx, name, []byte(testSchema), jsontext.Value(`{"api_key":"k-123"}`)); err != nil {
		t.Fatal(err)
	}
	raw := rawKey(t, ctx, pool, name, "api_key")
	if !strings.HasPrefix(raw, SealedPrefix) || strings.Contains(raw, "k-123") {
		t.Fatal("api_key is not stored sealed")
	}
	if got := settingsGetJSON(t, ctx, store, name)["api_key"]; string(got) != `"k-123"` {
		t.Fatalf("settings.get api_key = %s, want the plain value", got)
	}
}

func TestSettingsStoreBlankSaveKeepsSealed(t *testing.T) {
	ctx, pool := settingsTestPool(t)
	const name = "it-blank-keeps"
	seed(t, ctx, pool, name, `{}`)
	store := NewSettingsStore(pool, keyFor("k"), nil)
	schema := []byte(testSchema)
	save := func(body string) {
		t.Helper()
		old, err := store.Get(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, name, schema, PreserveWriteOnly(schema, old, jsontext.Value(body))); err != nil {
			t.Fatal(err)
		}
	}

	save(`{"api_key":"k-123"}`)
	save(`{"api_key":""}`)
	if raw := rawKey(t, ctx, pool, name, "api_key"); !strings.HasPrefix(raw, SealedPrefix) {
		t.Fatal("blank save lost the sealed value")
	}
	if got := settingsGetJSON(t, ctx, store, name)["api_key"]; string(got) != `"k-123"` {
		t.Fatalf("after blank save api_key = %s, want it kept", got)
	}

	save(`{"api_key":null}`)
	if raw := rawRow(t, ctx, pool, name); strings.Contains(raw, "api_key") {
		t.Fatalf("null did not clear the key: %s", raw)
	}
	if _, ok := settingsGetJSON(t, ctx, store, name)["api_key"]; ok {
		t.Fatal("api_key still readable after null")
	}
}

func TestSettingsStoreUnreadableUnderOtherSecret(t *testing.T) {
	ctx, pool := settingsTestPool(t)
	const name = "it-unreadable"
	seed(t, ctx, pool, name, `{}`)
	schema := []byte(testSchema)
	if err := NewSettingsStore(pool, keyFor("x"), nil).Put(ctx, name, schema, jsontext.Value(`{"api_key":"k-123","greeting":"hey"}`)); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	other := NewSettingsStore(pool, keyFor("y"), slog.New(slog.NewTextHandler(&logs, nil)))
	got, bad, err := other.GetChecked(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	obj := parseObject(got)
	if _, ok := obj["api_key"]; ok || string(obj["greeting"]) != `"hey"` {
		t.Fatalf("GetChecked = %s, want api_key omitted and greeting kept", got)
	}
	if len(bad) != 1 || bad[0] != "api_key" {
		t.Fatalf("unreadable = %v", bad)
	}
	if _, err := other.Get(ctx, name); err != nil {
		t.Fatal(err)
	}
	line := logs.String()
	if n := strings.Count(line, "level=WARN"); n != 1 || !strings.Contains(line, "plugin="+name) || !strings.Contains(line, "key=api_key") || strings.Contains(line, SealedPrefix) {
		t.Fatalf("want exactly one WARN naming plugin and key, got %q", line)
	}
	if _, ok := settingsGetJSON(t, ctx, other, name)["api_key"]; ok {
		t.Fatal("settings.get returned an unreadable key")
	}

	// A blank save has nothing readable to keep: the key is dropped.
	if err := other.Put(ctx, name, schema, PreserveWriteOnly(schema, got, jsontext.Value(`{"api_key":""}`))); err != nil {
		t.Fatal(err)
	}
	if raw := rawRow(t, ctx, pool, name); strings.Contains(raw, "api_key") {
		t.Fatalf("blank save kept an unreadable key: %s", raw)
	}
}
