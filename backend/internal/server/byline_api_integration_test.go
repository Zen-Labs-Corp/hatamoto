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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// The byline is writable through REST PATCH and MCP entities_update, so
// bylines can be re-pointed through public APIs.
func TestBylineAPIs_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "byline-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Byline IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Byline IT")
	t.Setenv("PUBLIC_BASE_URL", "https://byline-it.example.test")
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sfx := uuid.NewString()[:6]
	userID, postID, personID, otherID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "byline-"+sfx+"@example.test")
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		SELECT $1, id, 'Pat Person', $2::text, 'published', '{}'::jsonb FROM hatamoto_entity_schemas WHERE slug = 'person'`, personID, "pat-"+sfx)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		SELECT $1, id, 'A post', $2::text, 'draft', '{}'::jsonb FROM hatamoto_entity_schemas WHERE slug = 'post'`, postID, "post-"+sfx)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		SELECT $1, id, 'Not a person', $2::text, 'draft', '{}'::jsonb FROM hatamoto_entity_schemas WHERE slug = 'post'`, otherID, "other-"+sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, []uuid.UUID{postID, personID, otherID})
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_authors WHERE slug = $1`, "pat-"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	key, err := srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: "byline" + sfx, Scopes: []auth.APIKeyScope{auth.ScopeWriteEntities, auth.ScopeReadEntities}})
	if err != nil {
		t.Fatal(err)
	}
	state := func() (person *uuid.UUID, org bool, author *uuid.UUID) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, `SELECT byline_person_id, byline_org, author_id FROM hatamoto_entities WHERE id = $1`, postID).Scan(&person, &org, &author); err != nil {
			t.Fatal(err)
		}
		return
	}

	// REST PATCH.
	patch := func(id uuid.UUID, body string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/entities/"+id.String(), bytes.NewReader([]byte(body)))
		req.Header.Set("X-API-Key", key.PlainKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := patch(postID, `{"byline_person_id":"`+personID.String()+`"}`); code != 200 {
		t.Fatalf("PATCH byline_person_id = %d", code)
	}
	if p, org, a := state(); p == nil || *p != personID || org || a == nil {
		t.Errorf("REST person byline: person=%v org=%v author_id=%v (the mirror author_id must follow)", p, org, a)
	}
	if code := patch(postID, `{"byline_org":true}`); code != 200 {
		t.Fatalf("PATCH byline_org = %d", code)
	}
	if p, org, _ := state(); p != nil || !org {
		t.Errorf("REST org byline: person=%v org=%v", p, org)
	}
	if code := patch(postID, `{"byline_person_id":"`+otherID.String()+`"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("PATCH with a non-person id = %d, want 422", code)
	}
	if code := patch(postID, `{"clear_author":true}`); code != 200 {
		t.Fatalf("PATCH clear_author = %d", code)
	}
	if p, org, a := state(); p != nil || org || a != nil {
		t.Errorf("REST cleared: person=%v org=%v author_id=%v", p, org, a)
	}

	// MCP entities_update.
	c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{key.PlainKey}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	call := func(args map[string]any) (map[string]any, string) {
		t.Helper()
		args["id"] = postID.String()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "entities_update", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			return nil, text
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%v\n%s", err, text)
		}
		return out, ""
	}
	out, errText := call(map[string]any{"byline_person_id": personID.String()})
	if errText != "" || out["byline_person_id"] != personID.String() {
		t.Errorf("MCP set person: %v %q", out, errText)
	}
	if p, org, a := state(); p == nil || *p != personID || org || a == nil {
		t.Errorf("MCP person byline: person=%v org=%v author_id=%v", p, org, a)
	}
	out, errText = call(map[string]any{"byline_org": true})
	if errText != "" || out["byline_org"] != true {
		t.Errorf("MCP set org: %v %q", out, errText)
	}
	if p, org, _ := state(); p != nil || !org {
		t.Errorf("MCP org byline: person=%v org=%v", p, org)
	}
	if _, errText = call(map[string]any{"byline_person_id": otherID.String()}); errText == "" {
		t.Error("MCP accepted a non-person id")
	}
	if _, errText = call(map[string]any{"clear_byline": true}); errText != "" {
		t.Errorf("MCP clear: %s", errText)
	}
	if p, org, a := state(); p != nil || org || a != nil {
		t.Errorf("MCP cleared: person=%v org=%v author_id=%v", p, org, a)
	}
}
