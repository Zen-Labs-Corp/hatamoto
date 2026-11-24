//go:build integration

package app

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
)

// The CLI's package manager must create the content type a plugin owns
// (entity_schema), as the server does, or a kit that fills that type cannot
// finish.
func TestKitApplyPluginOwnedSchema_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("PUBLIC_BASE_URL", "")

	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{}
	now := time.Now().UTC()
	idx := packages.Index{Version: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339)}
	publish := func(kind packages.Kind, name, version string, files map[string][]byte) {
		raw, err := packages.PackFiles(files, &key)
		if err != nil {
			t.Fatal(err)
		}
		p, err := packages.Parse(raw, packages.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		sum := p.SHA256()
		path := "/" + name + "-" + string(kind) + ".hmp"
		blobs[path] = raw
		idx.Packages = append(idx.Packages, packages.Entry{Kind: kind, Name: name, Version: version, URL: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	}
	publish(packages.KindPlugin, "board", "1.0.0", map[string][]byte{
		"plugin.json":        []byte(`{"name":"board","version":"1.0.0","sdk":"^0.27","permissions":["entities.write.own"],"entity_schema":"story"}`),
		"plugin.wasm":        []byte("\x00asm\x01\x00\x00\x00"),
		"schemas/story.json": []byte(`{"title":"Stories","type":"object","properties":{"url":{"type":"string"}}}`),
	})
	publish(packages.KindKit, "boardkit", "1.0.0", map[string][]byte{"kit.json": []byte(`{
  "name": "boardkit", "version": "1.0.0", "kit": 1, "sdk": "^0.27", "title": "Board",
  "plugins": [{"name": "board", "range": "^1", "grant": "all"}],
  "schemas": [{"slug": "story", "name": "Stories", "schema": {"type": "object"}, "settings": {"url_pattern": "/item/{slug}"}}]
}`)})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registry.json" {
			b, _ := jsonx.Marshal(idx)
			doc, err := packages.SignIndex(b, key)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		if b, ok := blobs[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	saved := registryClientOptions
	registryClientOptions.AllowPrivate = true
	t.Cleanup(func() { registryClientOptions = saved })
	t.Setenv("HATAMOTO_REGISTRY_URL", srv.URL+"/registry.json")
	t.Setenv("HATAMOTO_REGISTRY_ALLOW_HTTP", "true")
	t.Setenv("HATAMOTO_TRUSTED_KEYS", key.Public)
	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "off")

	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	out, err := kitCLI(t, "apply", "boardkit")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "applied.") {
		t.Errorf("output lacks \"applied.\":\n%s", out)
	}
	var origin string
	if err := db.Pool().QueryRow(ctx, `SELECT COALESCE(origin_plugin, '') FROM hatamoto_entity_schemas WHERE slug = 'story'`).Scan(&origin); err != nil {
		t.Fatalf("the plugin did not create its content type: %v", err)
	}
	if origin != "board" {
		t.Errorf("content type origin = %q, want board", origin)
	}
}
