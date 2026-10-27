//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Characterization of the REST entity payload (the shape clients and plugins
// read) and of per-locale slug uniqueness over REST. Regenerate with
// DATABASE_URL=... go test -tags integration ./internal/server -run EntityRESTPayloadGolden -update
func TestEntityRESTPayloadGolden(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "erg-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Entity REST IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Entity REST IT")
	t.Setenv("PUBLIC_BASE_URL", "https://erg-it.example.test")
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// Fixed names (the golden embeds them); cleaned up before and after.
	const schemaSlug = "erg-note"
	sfx := uuid.NewString()[:6]
	userID, schemaID := uuid.New(), uuid.New()
	cleanup := func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id IN (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1)`, schemaSlug)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE slug = $1`, schemaSlug)
	}
	cleanup()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "erg-"+sfx+"@example.test")
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'ERG Note',$2,'{"type":"object"}','{}')`, schemaID, schemaSlug)
	t.Cleanup(func() {
		cleanup()
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	key, err := srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: "erg" + sfx, Scopes: []auth.APIKeyScope{auth.ScopeWriteEntities, auth.ScopeReadEntities}})
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("X-API-Key", key.PlainKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}
	create := func(slug, locale string) (int, []byte) {
		body := map[string]any{"schema_id": schemaID, "name": "About", "slug": slug, "attributes": map[string]any{}}
		if locale != "" {
			body["locale"] = locale
		}
		b, _ := json.Marshal(body)
		return do(http.MethodPost, "/api/v1/entities", string(b))
	}

	code, en := create("about", "en")
	if code != http.StatusCreated {
		t.Fatalf("POST en = %d: %s", code, en)
	}
	if code, raw := create("about", "fr"); code != http.StatusCreated {
		t.Fatalf("POST fr (same slug, other locale) = %d: %s", code, raw)
	}
	code, raw := create("about", "en")
	if code != http.StatusConflict {
		t.Fatalf("third POST en = %d, want 409: %s", code, raw)
	}
	var apiErr struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &apiErr); err != nil || apiErr.Code != "slug_exists" {
		t.Errorf("conflict body = %s, want code slug_exists", raw)
	}
	if code, raw := create("bad", "x"); code != http.StatusBadRequest {
		t.Errorf("POST invalid locale = %d, want 400: %s", code, raw)
	}
	if code, raw := create("long", "en-"+strings.Repeat("a", 40)); code != http.StatusBadRequest {
		t.Errorf("POST over-long locale = %d, want 400: %s", code, raw)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(en, &created); err != nil {
		t.Fatalf("decode create: %v\n%s", err, en)
	}
	code, body := do(http.MethodGet, "/api/v1/entities/"+created.ID.String(), "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d: %s", code, body)
	}
	got := normalizePublicJSON(t, body)
	path := filepath.Join("testdata", "entity_rest", "entity.json")
	if *updatePublicJSON {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("entity REST payload drifted from golden.\n got: %s\nwant: %s", got, want)
	}
}
