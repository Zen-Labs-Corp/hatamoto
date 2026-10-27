//go:build integration

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// bearer adds an API key to every request of an MCP client.
type bearer struct{ key string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.key != "" {
		r.Header.Set("Authorization", "Bearer "+b.key)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// An MCP client drafts, publishes and reads AI-crawler activity for a page,
// with scoped keys, against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run MCPIntegration
func TestMCPIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "mcp-it")
	t.Setenv("HATAMOTO_SITE_NAME", "MCP IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "MCP IT")
	t.Setenv("PUBLIC_BASE_URL", "https://mcp-it.example.test")
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
	schemaSlug := "mcpit-" + sfx
	schemaID, userID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "mcp-"+sfx+"@example.test")
	var hadSite bool
	if err := db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_site_config)`).Scan(&hadSite); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('MCP IT') ON CONFLICT (id) DO NOTHING`)
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "/mcp-old-"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_terms WHERE slug=$1`, "mcp-tag-"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_menus WHERE slug=$1`, "mcp-menu-"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)

	mkKey := func(name string, scopes ...auth.APIKeyScope) string {
		k, err := srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: name + sfx, Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return k.PlainKey
	}
	connect := func(key string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
		sess, err := c.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{key}}}, nil)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { sess.Close() })
		return sess
	}
	call := func(sess *mcp.ClientSession, tool string, args any) (map[string]any, error) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: protocol error: %v", tool, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			return nil, &toolErr{text}
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%s: %v\n%s", tool, err, text)
		}
		return out, nil
	}
	mustCall := func(sess *mcp.ClientSession, tool string, args any) map[string]any {
		t.Helper()
		out, err := call(sess, tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	// No key, or a bad one: 401 before any MCP traffic.
	for _, k := range []string{"", "nxk_" + strings.Repeat("0", 40)} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
		if k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("key %q: status %d, want 401", k, resp.StatusCode)
		}
	}

	rw := connect(mkKey("rw", auth.ScopeWriteEntities, auth.ScopeReadReports,
		auth.ScopeWriteMedia, auth.ScopeWriteMenus, auth.ScopeWriteRedirects, auth.ScopeWriteTerms))

	// Behind Caddy the connection is loopback but Host is the public name; a
	// foreign Origin is refused, a missing one (non-browser client) is fine.
	for origin, want := range map[string]int{"": 200, "https://mcp-it.example.test": 200, "https://evil.example": 403} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"it","version":"1"}}}`))
		req.Host = "hatamoto.dev"
		req.Header.Set("Authorization", "Bearer "+mkKey("host", auth.ScopeReadEntities))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host hatamoto.dev, Origin %q: status %d, want %d", origin, resp.StatusCode, want)
		}
	}

	// A publishing key (write:entities) cannot touch media, menus, redirects or terms.
	pubKey := connect(mkKey("pubonly", auth.ScopeWriteEntities))
	for tool, args := range map[string]any{
		"media_upload":     map[string]any{"filename": "a.png", "data_base64": "AA=="},
		"terms_create":     map[string]any{"kind": "tag", "name": "x"},
		"menus_update":     map[string]any{"id": uuid.NewString()},
		"redirects_create": map[string]any{"source_path": "/a", "target_url": "/b"},
		"redirects_delete": map[string]any{"id": uuid.NewString()},
	} {
		if _, err := call(pubKey, tool, args); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("write:entities key on %s: %v", tool, err)
		}
	}
	tools, err := rw.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 20 {
		t.Errorf("only %d tools listed", len(tools.Tools))
	}

	// Draft -> read -> publish -> render.
	created := mustCall(rw, "entities_create", map[string]any{
		"schema": schemaSlug, "name": "MCP Page " + sfx, "slug": "mcp-page-" + sfx,
		"attributes": map[string]any{"title": "Hello", "page_sections": []any{
			map[string]any{"id": "h1", "type": "html", "sort_order": 1, "content": map[string]any{"body": "<p>Agent text</p><script>alert(1)</script>"}}}},
	})
	if created["status"] != "draft" {
		t.Fatalf("created status = %v, want draft", created["status"])
	}
	id := created["id"].(string)
	if resp, _ := http.Get(ts.URL + "/mcp-page-" + sfx); resp.StatusCode != 404 {
		t.Errorf("draft is public: %d", resp.StatusCode)
	}
	got := mustCall(rw, "entities_get", map[string]any{"id": id})
	if strings.Contains(string(mustJSON(t, got)), "<script>") {
		t.Error("script survived the save-time sanitiser")
	}
	upd := mustCall(rw, "entities_update", map[string]any{"id": id, "name": "MCP Page v2 " + sfx,
		"attributes": map[string]any{"title": "Hello again"}, "change_summary": "retitle"})
	if upd["name"] != "MCP Page v2 "+sfx {
		t.Errorf("update: %v", upd)
	}
	pub := mustCall(rw, "entities_publish", map[string]any{"id": id})
	if pub["status"] != "published" || pub["url"] != "/mcp-page-"+sfx {
		t.Errorf("publish: %v", pub)
	}
	page, _ := http.Get(ts.URL + "/mcp-page-" + sfx)
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 {
		t.Fatalf("published page: %d", page.StatusCode)
	}

	// An AI crawler fetches the page; the agent sees it in the analytics tools.
	greq, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp-page-"+sfx, nil)
	greq.Header.Set("User-Agent", "Mozilla/5.0 AppleWebKit/537.36 (compatible; GPTBot/1.1; +https://openai.com/gptbot)")
	if r, err := http.DefaultClient.Do(greq); err != nil || r.StatusCode != 200 {
		t.Fatalf("crawler fetch: %v", err)
	}
	var hits map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for {
		hits = mustCall(rw, "analytics_crawler_hits", map[string]any{"entity_id": id})
		if pages, _ := hits["pages"].([]any); containsPath(pages, "/mcp-page-"+sfx) || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !containsPath(hits["pages"].([]any), "/mcp-page-"+sfx) {
		t.Fatalf("crawler hit never reported: %v", hits)
	}
	if len(hits["agents"].([]any)) == 0 {
		t.Error("no agent totals")
	}
	for range 20 { // the first-crawl tracker runs off the writer
		fc := mustCall(rw, "analytics_first_crawls", map[string]any{})
		if items, _ := fc["items"].([]any); len(items) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if ref := mustCall(rw, "analytics_ai_referrals", map[string]any{}); ref["days"] == nil {
		t.Errorf("referrals: %v", ref)
	}

	// Previews and JSON-LD.
	sm := mustCall(rw, "sitemap_preview", map[string]any{})
	if !strings.Contains(sm["body"].(string), "<sitemapindex") {
		t.Errorf("sitemap preview: %v", sm)
	}
	if ll := mustCall(rw, "llms_txt_preview", map[string]any{}); !strings.Contains(ll["body"].(string), "llms") && ll["status"].(float64) != 200 {
		t.Errorf("llms preview: %v", ll)
	}
	ld := mustCall(rw, "jsonld_validate", map[string]any{"entity_id": id})
	if ld["source"] != "rendered page" || ld["report"].(map[string]any)["blocks"].(float64) < 1 {
		t.Errorf("jsonld: %v (page had ld+json: %v)", ld, strings.Contains(string(html), "ld+json"))
	}
	if s := mustCall(rw, "search", map[string]any{"query": "again"}); s["total"] == nil {
		t.Errorf("search: %v", s)
	}

	// Redirects, terms, menus.
	rd := mustCall(rw, "redirects_create", map[string]any{"source_path": "/mcp-old-" + sfx, "target_url": "/mcp-page-" + sfx})
	if r, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(ts.URL + "/mcp-old-" + sfx); err != nil || r.StatusCode != 301 {
		t.Errorf("redirect not live: %v %v", r, err)
	}
	mustCall(rw, "redirects_delete", map[string]any{"id": rd["id"]})
	if _, err := call(rw, "redirects_create", map[string]any{"source_path": "/x-" + sfx, "target_url": "//evil.example"}); err == nil {
		t.Log("note: protocol-relative redirect target accepted by the service (same as the API)")
	}
	mustCall(rw, "terms_create", map[string]any{"kind": "tag", "name": "MCP Tag " + sfx, "slug": "mcp-tag-" + sfx})
	if _, err := call(rw, "terms_create", map[string]any{"kind": "Not A Kind", "name": "x"}); err == nil {
		t.Error("invalid term kind accepted")
	}
	menuRow := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_menus (name, slug) VALUES ('MCP menu', $1) RETURNING id`, "mcp-menu-"+sfx)
	var menuID uuid.UUID
	if err := menuRow.Scan(&menuID); err != nil {
		t.Fatal(err)
	}
	m := mustCall(rw, "menus_update", map[string]any{"id": menuID.String(), "items": []any{
		map[string]any{"key": "a", "label": "Page", "link_type": "entity", "entity_id": id},
		map[string]any{"key": "b", "parent_key": "a", "label": "Out", "link_type": "external", "url": "https://example.test/"},
	}})
	if items, _ := m["items"].([]any); len(items) != 1 {
		t.Errorf("menu items: %v", m["items"])
	}
	if _, err := call(rw, "menus_update", map[string]any{"id": menuID.String(), "items": []any{
		map[string]any{"label": "Bad", "link_type": "nope"}}}); err == nil {
		t.Error("bad menu item accepted")
	}
	if g := mustCall(rw, "menus_get", map[string]any{"id": menuID.String()}); len(g["items"].([]any)) != 1 {
		t.Errorf("a rejected update must leave the menu alone: %v", g)
	}
	// A well-formed item whose entity does not exist fails in the database;
	// the live menu (and its name) must survive that.
	if _, err := call(rw, "menus_update", map[string]any{"id": menuID.String(), "name": "Renamed", "items": []any{
		map[string]any{"label": "Ghost", "link_type": "entity", "entity_id": uuid.NewString()}}}); err == nil || !strings.Contains(err.Error(), "entity that does not exist") {
		t.Errorf("missing entity: %v", err)
	}
	g := mustCall(rw, "menus_get", map[string]any{"id": menuID.String()})
	if len(g["items"].([]any)) != 1 || g["name"] == "Renamed" {
		t.Errorf("failed menus_update changed the menu: %v", g)
	}
	// Each site scope alone is enough for its tool.
	mustCall(connect(mkKey("terms", auth.ScopeWriteTerms)), "terms_create", map[string]any{"kind": "tag", "name": "MCP Tag2 " + sfx, "slug": "mcp-tag-" + sfx + "-2"})
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_terms WHERE slug=$1`, "mcp-tag-"+sfx+"-2")
	})
	mustCall(connect(mkKey("menus", auth.ScopeWriteMenus)), "menus_update", map[string]any{"id": menuID.String(), "name": "MCP menu"})
	rd2 := mustCall(connect(mkKey("redirects", auth.ScopeWriteRedirects)), "redirects_create", map[string]any{"source_path": "/mcp-old-" + sfx + "b", "target_url": "/x"})
	mustCall(connect(mkKey("redirects2", auth.ScopeWriteRedirects)), "redirects_delete", map[string]any{"id": rd2["id"]})

	// Media: png bytes upload; html named .png is refused.
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	up := mustCall(rw, "media_upload", map[string]any{"filename": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(png), "alt_text": "dot"})
	if up["mime_type"] != "image/png" {
		t.Errorf("upload: %v", up)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_media WHERE id=$1`, up["id"])
	})
	if _, err := call(rw, "media_upload", map[string]any{"filename": "x.png", "data_base64": base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script></html>"))}); err == nil {
		t.Error("html disguised as png accepted")
	}
	// The HTTP body cap may end the session, so use a throwaway one.
	if res, err := connect(mkKey("big", auth.ScopeWriteEntities)).CallTool(ctx, &mcp.CallToolParams{Name: "media_upload", Arguments: map[string]any{"filename": "big.png", "data_base64": strings.Repeat("A", 20<<20)}}); err == nil && !res.IsError {
		t.Error("oversized upload accepted")
	}

	// A read-only key reads but can never write; reports need their own scope.
	ro := connect(mkKey("ro", auth.ScopeReadEntities))
	mustCall(ro, "entities_list", map[string]any{})
	mustCall(ro, "schemas_get", map[string]any{"slug": schemaSlug})
	for tool, args := range map[string]any{
		"entities_create":        map[string]any{"schema": schemaSlug, "name": "nope"},
		"entities_publish":       map[string]any{"id": id},
		"entities_unpublish":     map[string]any{"id": id},
		"redirects_create":       map[string]any{"source_path": "/a", "target_url": "/b"},
		"terms_create":           map[string]any{"kind": "tag", "name": "x"},
		"media_upload":           map[string]any{"filename": "a.png", "data_base64": "AA=="},
		"analytics_ai_referrals": map[string]any{},
	} {
		if _, err := call(ro, tool, args); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("read-only key on %s: %v", tool, err)
		}
	}
	if e := mustCall(ro, "entities_get", map[string]any{"id": id}); e["entity"].(map[string]any)["status"] != "published" {
		t.Errorf("read-only key changed state: %v", e)
	}

	// Unpublish takes the page down; every call is in the audit log.
	mustCall(rw, "entities_unpublish", map[string]any{"id": id})
	if r, _ := http.Get(ts.URL + "/mcp-page-" + sfx); r.StatusCode != 404 {
		t.Errorf("unpublished page still served: %d", r.StatusCode)
	}
	var calls, failed int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT ok) FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx).Scan(&calls, &failed); err != nil {
		t.Fatal(err)
	}
	if calls < 30 || failed < 7 {
		t.Errorf("audit rows: %d calls, %d failed", calls, failed)
	}
	var args string
	if err := db.Pool().QueryRow(ctx, `SELECT args::text FROM hatamoto_mcp_audit WHERE tool='media_upload' AND ok AND api_key_name LIKE $1`, "%"+sfx).Scan(&args); err != nil || len(args) > 600 {
		t.Errorf("media args not shortened: %d bytes (%v)", len(args), err)
	}
}

type toolErr struct{ msg string }

func (e *toolErr) Error() string { return e.msg }

func containsPath(pages []any, path string) bool {
	for _, p := range pages {
		if p.(map[string]any)["path"] == path {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
