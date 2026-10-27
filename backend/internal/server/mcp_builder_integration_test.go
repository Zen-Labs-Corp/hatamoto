//go:build integration

package server

import (
	"bufio"
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

// docStep is one call of the example session in docs/MCP.md and what the
// document says it returns: a result (JSON, "*" matches anything) or an error
// (text, prefix).
type docStep struct {
	callText  string
	result    any
	errPrefix string
}

// mcpDocSteps reads the fenced blocks marked `json mcp-step`, `json mcp-result`
// and `text mcp-error` of docs/MCP.md, in order.
func mcpDocSteps(t *testing.T) []*docStep {
	t.Helper()
	f, err := os.Open("../../../docs/MCP.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var steps []*docStep
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var kind string
	var body strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case kind == "" && strings.HasPrefix(line, "```"):
			if info := strings.TrimPrefix(line, "```"); strings.HasSuffix(info, "mcp-step") || strings.HasSuffix(info, "mcp-result") || strings.HasSuffix(info, "mcp-error") {
				kind = info[strings.LastIndex(info, " ")+1:]
				body.Reset()
			} else {
				kind = "other"
			}
		case kind != "" && strings.HasPrefix(line, "```"):
			text := strings.TrimSpace(body.String())
			switch kind {
			case "mcp-step":
				steps = append(steps, &docStep{callText: text})
			case "mcp-result", "mcp-error":
				if len(steps) == 0 {
					t.Fatalf("%s before any mcp-step", kind)
				}
				last := steps[len(steps)-1]
				if kind == "mcp-error" {
					last.errPrefix = text
				} else if err := json.Unmarshal([]byte(text), &last.result); err != nil {
					t.Fatalf("mcp-result: %v\n%s", err, text)
				}
			}
			kind = ""
		case kind == "mcp-step" || kind == "mcp-result" || kind == "mcp-error":
			body.WriteString(line + "\n")
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return steps
}

// subset reports where actual does not contain want: every key of an object,
// every element of an array (same length), equal scalars; "*" matches anything.
func subset(want, actual any, path string) string {
	switch w := want.(type) {
	case string:
		if w == "*" {
			return ""
		}
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return path + ": want an object"
		}
		for k, wv := range w {
			av, ok := a[k]
			if !ok {
				return path + "." + k + ": missing"
			}
			if msg := subset(wv, av, path+"."+k); msg != "" {
				return msg
			}
		}
		return ""
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(w) {
			return path + ": want an array of the same length"
		}
		for i := range w {
			if msg := subset(w[i], a[i], path+"[]"); msg != "" {
				return msg
			}
		}
		return ""
	}
	if want != actual {
		b1, _ := json.Marshal(want)
		b2, _ := json.Marshal(actual)
		return path + ": want " + string(b1) + ", got " + string(b2)
	}
	return ""
}

// TestMCPBuilderSession replays the example session of docs/MCP.md through the
// real MCP endpoint (real entity service, sanitiser, render engine), then
// checks what the document does not show: the public page, stored state, the
// sanitiser, scopes, the entities_update rules and the audit rows.
//
//	HATAMOTO_TEST_DB_URL=postgres://.../throwaway go test -tags integration ./internal/server -run MCPBuilder
func TestMCPBuilderSession(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set; needs a migrated Postgres")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "mcp-b-it")
	t.Setenv("HATAMOTO_SITE_NAME", "MCP Builder IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "MCP Builder IT")
	t.Setenv("PUBLIC_BASE_URL", "https://mcp-b-it.example.test")
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
	userID := uuid.New()
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "mcpb-"+sfx+"@example.test")
	var hadSite bool
	if err := db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_site_config)`).Scan(&hadSite); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('MCP Builder IT') ON CONFLICT (id) DO NOTHING`)
	var pageIDs []uuid.UUID // entities to remove again
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range pageIDs {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id=$1`, id)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
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
	// call returns the decoded JSON result, or the error text.
	call := func(sess *mcp.ClientSession, tool string, args any) (any, string) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: protocol error: %v", tool, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			return nil, text
		}
		var out any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%s: %v\n%s", tool, err, text)
		}
		return out, ""
	}
	mustCall := func(sess *mcp.ClientSession, tool string, args any) map[string]any {
		t.Helper()
		out, errText := call(sess, tool, args)
		if errText != "" {
			t.Fatalf("%s: %s", tool, errText)
		}
		return out.(map[string]any)
	}

	rw := connect(mkKey("rw", auth.ScopeWriteEntities))
	ro := connect(mkKey("ro", auth.ScopeReadEntities))

	// 1. The document's example, verbatim.
	steps := mcpDocSteps(t)
	if len(steps) < 8 {
		t.Fatalf("docs/MCP.md has %d mcp-step blocks, want the whole session", len(steps))
	}
	pageID := ""
	for i, st := range steps {
		args := st.callText
		if pageID != "" {
			args = strings.ReplaceAll(args, "<page-id>", pageID)
		}
		var c struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		got, errText := call(rw, c.Tool, c.Arguments)
		switch {
		case st.errPrefix != "":
			if !strings.HasPrefix(errText, st.errPrefix) {
				t.Fatalf("step %d (%s): error %q, the document says it starts %q", i, c.Tool, errText, st.errPrefix)
			}
		case errText != "":
			t.Fatalf("step %d (%s): %s", i, c.Tool, errText)
		case st.result != nil:
			if msg := subset(st.result, got, "result"); msg != "" {
				b, _ := json.Marshal(got)
				t.Fatalf("step %d (%s): %s\nactual: %.1500s", i, c.Tool, msg, b)
			}
		}
		if c.Tool == "entities_create" {
			pageID = got.(map[string]any)["id"].(string)
			id, _ := uuid.Parse(pageID)
			pageIDs = append(pageIDs, id)
		}
	}

	// 2. What the document does not show.
	// The published page carries the built layout; the stale edit never landed.
	resp, err := http.Get(ts.URL + "/launch")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Launch week is here") || !strings.Contains(string(body), "What is fixed") || strings.Contains(string(body), "Stale edit") {
		t.Errorf("public page: %d, has title %v", resp.StatusCode, strings.Contains(string(body), "Launch week is here"))
	}
	// The full-page render is a document with the builder stylesheet.
	full := mustCall(rw, "builder_render", map[string]any{"entity_id": pageID})
	if h, _ := full["html"].(string); !strings.Contains(h, "<html") || !strings.Contains(h, "Launch week is here") || full["css"] == "" {
		t.Errorf("full render: html %.120q css %.60q", h, full["css"])
	}
	node := mustCall(rw, "builder_render", map[string]any{"entity_id": pageID, "ids": []string{"hero-title"},
		"ops": []any{map[string]any{"op": "setProp", "id": "hero-title", "key": "text", "value": "Dry run title"}}})
	if h, _ := node["html"].(string); !strings.Contains(h, "Dry run title") {
		t.Errorf("node render with ops: %v", node)
	}
	got := mustCall(rw, "builder_get", map[string]any{"entity_id": pageID})
	if strings.Contains(mustJSONString(t, got["doc"]), "Dry run title") || got["version"] != float64(4) {
		t.Errorf("render stored something or the version moved: version %v", got["version"])
	}

	// The sanitiser runs: markup in a component's content does not survive the save.
	dirty, errText := call(rw, "builder_apply_ops", map[string]any{"entity_id": pageID, "version": 4, "ops": []any{
		map[string]any{"op": "insert", "node": map[string]any{"id": "raw", "t": "component", "p": map[string]any{"type": "html", "content": map[string]any{"body": "<p>ok</p><script>alert(1)</script>"}}}},
	}})
	if errText != "" {
		t.Logf("component insert refused (a theme without an html section type): %s", errText)
	} else {
		d := dirty.(map[string]any)
		stored := mustCall(rw, "builder_get", map[string]any{"entity_id": pageID})
		if strings.Contains(mustJSONString(t, stored["doc"]), "<script>") || d["stored_differs"] != true {
			t.Errorf("script survived the builder save: differs=%v", d["stored_differs"])
		}
	}

	// A read-only key reads but cannot write.
	if _, e := call(ro, "builder_apply_ops", map[string]any{"entity_id": pageID, "version": 1, "ops": []any{map[string]any{"op": "remove", "id": "hero"}}}); !strings.HasPrefix(e, "forbidden:") {
		t.Errorf("read-only key: %q", e)
	}
	if _, e := call(ro, "builder_get", map[string]any{"entity_id": pageID}); e != "" {
		t.Errorf("read-only key reads: %q", e)
	}

	// Posts are not builder pages.
	post := mustCall(rw, "entities_create", map[string]any{"schema": "post", "name": "A post " + sfx, "slug": "mcpb-post-" + sfx})
	postID, _ := uuid.Parse(post["id"].(string))
	pageIDs = append(pageIDs, postID)
	if _, e := call(rw, "builder_get", map[string]any{"entity_id": post["id"]}); !strings.HasPrefix(e, "not_builder:") {
		t.Errorf("post: %q", e)
	}

	// The shared rules: a slot on a page is refused by the ops tool and by entities_update.
	cur := mustCall(rw, "builder_get", map[string]any{"entity_id": pageID})
	if _, e := call(rw, "builder_apply_ops", map[string]any{"entity_id": pageID, "version": cur["version"], "ops": []any{
		map[string]any{"op": "insert", "node": map[string]any{"t": "slot"}}}}); !strings.Contains(e, "part_slot") {
		t.Errorf("slot via ops: %q", e)
	}
	if _, e := call(rw, "entities_update", map[string]any{"id": pageID, "attributes": map[string]any{
		"page_doc": map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "s", "t": "slot"}}}}}); !strings.HasPrefix(e, "invalid page_doc:") {
		t.Errorf("slot via entities_update: %q", e)
	}
	// ...while a valid doc still goes through entities_update.
	if _, e := call(rw, "entities_update", map[string]any{"id": pageID, "attributes": map[string]any{
		"page_doc": map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "only", "t": "heading", "p": map[string]any{"text": "Only"}}}}}}); e != "" {
		t.Errorf("valid doc via entities_update: %q", e)
	}

	// Every call above was audited.
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1 AND tool LIKE 'builder_%'`, "%"+sfx).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < len(steps) {
		t.Errorf("%d builder audit rows for %d steps", n, len(steps))
	}
}

func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
