//go:build integration

package server

import (
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

// A7: a page created over the API with a 2-column page_doc renders both
// columns at its public URL; an invalid doc is a 422 on REST and a readable
// message on MCP:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PageDocIntegration
func TestPageDocIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "pd-it")
	t.Setenv("HATAMOTO_SITE_NAME", "PD IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "PD IT")
	t.Setenv("PUBLIC_BASE_URL", "https://pd-it.example.test")
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
	schemaSlug := "pdit-" + sfx
	schemaID, userID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "pd-"+sfx+"@example.test")
	var hadSite bool
	if err := db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_site_config)`).Scan(&hadSite); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('PD IT') ON CONFLICT (id) DO NOTHING`)
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	key, err := srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: "pd" + sfx, Scopes: []auth.APIKeyScope{auth.ScopeWriteEntities}})
	if err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{key.PlainKey}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	call := func(tool string, args any) (map[string]any, string, bool) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: protocol error: %v", tool, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		var out map[string]any
		if !res.IsError {
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("%s: %v\n%s", tool, err, text)
			}
		}
		return out, text, res.IsError
	}

	twoColumns := map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "row", "t": "container", "p": map[string]any{"tag": "div"},
			"s": map[string]any{"base": map[string]any{"cols": "2", "gap": "space-4"}},
			"c": []any{
				map[string]any{"id": "left", "t": "container", "p": map[string]any{"tag": "div"}, "c": []any{
					map[string]any{"id": "lh", "t": "heading", "p": map[string]any{"text": "Left column", "level": 2}}}},
				map[string]any{"id": "right", "t": "container", "p": map[string]any{"tag": "div"}, "c": []any{
					map[string]any{"id": "rt", "t": "text", "p": map[string]any{"text": "Right column"}}}},
			}}}}

	// ISC-14/15: MCP create + publish, public GET renders both columns natively.
	created, text, isErr := call("entities_create", map[string]any{
		"schema": schemaSlug, "name": "Doc " + sfx, "slug": "doc-" + sfx,
		"attributes": map[string]any{"page_doc": twoColumns},
	})
	if isErr {
		t.Fatalf("entities_create with page_doc: %s", text)
	}
	id := created["id"].(string)
	if _, text, isErr := call("entities_publish", map[string]any{"id": id}); isErr {
		t.Fatalf("publish: %s", text)
	}
	resp, err := http.Get(ts.URL + "/doc-" + sfx)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET: %d %.400s", resp.StatusCode, html)
	}
	for _, want := range []string{"Left column", "Right column", "hm-n-left", "hm-n-right"} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(html, "data-hm-node") {
		t.Error("public HTML carries data-hm-node markers")
	}
	if n := strings.Count(html, `id="hm-builder-css"`); n != 1 {
		t.Errorf("hm-builder-css style tags = %d, want 1", n)
	}
	var attrs string
	if err := db.Pool().QueryRow(ctx, `SELECT attributes::text FROM hatamoto_entities WHERE id=$1`, id).Scan(&attrs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(attrs, "page_sections") {
		t.Errorf("builder-only doc kept page_sections: %s", attrs)
	}

	// ISC-20: invalid doc -> readable MCP message and REST 422 invalid_page_doc.
	bad := map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "x", "t": "nosuchkind", "p": map[string]any{}}}}
	_, text, isErr = call("entities_create", map[string]any{
		"schema": schemaSlug, "name": "Bad " + sfx, "attributes": map[string]any{"page_doc": bad}})
	if !isErr || !strings.Contains(text, "page_doc") || strings.Contains(strings.ToLower(text), "internal error") {
		t.Errorf("MCP invalid doc: isErr=%v text=%q", isErr, text)
	}
	body422, _ := json.Marshal(map[string]any{"schema_id": schemaID, "name": "Bad2 " + sfx, "attributes": map[string]any{"page_doc": bad}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/entities", strings.NewReader(string(body422)))
	req.Header.Set("X-API-Key", key.PlainKey)
	req.Header.Set("Content-Type", "application/json")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b2), `"invalid_page_doc"`) {
		t.Errorf("REST invalid doc: %d %s", r2.StatusCode, b2)
	}

	// ISC-9: a changed page_sections on a page that has a doc is 422
	// page_sections_removed on REST and the same sentence on MCP; nothing is stored.
	sections := []any{map[string]any{"id": "s1", "type": "content", "sort_order": 0, "content": map[string]any{"body": "<p>x</p>"}}}
	put := func(attrs map[string]any) (int, string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"attributes": attrs})
		rq, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/entities/"+id, strings.NewReader(string(b)))
		rq.Header.Set("X-API-Key", key.PlainKey)
		rq.Header.Set("Content-Type", "application/json")
		rs, err := http.DefaultClient.Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(rs.Body)
		rs.Body.Close()
		return rs.StatusCode, string(out)
	}
	if code, out := put(map[string]any{"page_sections": sections}); code != http.StatusUnprocessableEntity || !strings.Contains(out, `"page_sections_removed"`) {
		t.Errorf("REST page_sections on a doc page: %d %s", code, out)
	}
	_, text, isErr = call("entities_update", map[string]any{"id": id, "attributes": map[string]any{"page_sections": sections}})
	if !isErr || !strings.Contains(text, "page_sections can no longer change a page that has a page_doc") {
		t.Errorf("MCP page_sections on a doc page: isErr=%v text=%q", isErr, text)
	}
	// both in one write: the doc wins, the sections are ignored
	changed := map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "only", "t": "heading", "p": map[string]any{"text": "Changed", "level": 1}}}}
	if code, out := put(map[string]any{"page_doc": changed, "page_sections": sections}); code != http.StatusOK {
		t.Errorf("REST doc + sections: %d %s", code, out)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT attributes::text FROM hatamoto_entities WHERE id=$1`, id).Scan(&attrs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(attrs, "page_sections") {
		t.Errorf("ignored page_sections was stored: %s", attrs)
	}
}
