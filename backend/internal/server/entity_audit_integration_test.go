//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/worker"
)

// Audit coverage across the write surfaces, through the production wiring of
// a full Server on a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run 'EntityWrite|AdminEntityWriteInTx|ScheduledPublishWiring|RESTContentWrites|MCPContentWrite|EntAudit'

const entAuditPassword = "correct horse battery staple 9"

type entAuditEnv struct {
	t          *testing.T
	ctx        context.Context
	db         *database.DB
	srv        *Server
	ts         *httptest.Server
	sfx        string
	owner      *user.User
	token      string // owner admin session
	schemaID   uuid.UUID
	schemaSlug string
	baseline   int64 // highest audit id before the test
	targets    []string
	actors     []string
	secrets    []string
}

func entAuditNew(t *testing.T) *entAuditEnv {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "entaudit-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Entity Audit IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Entity Audit IT")
	t.Setenv("PUBLIC_BASE_URL", "https://entaudit-it.example.test")
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.Resolved = "/admin"
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	e := &entAuditEnv{t: t, ctx: ctx, db: db, sfx: uuid.NewString()[:6], schemaID: uuid.New()}
	e.schemaSlug = "entaudit-" + e.sfx
	e.exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Entity Audit IT') ON CONFLICT (id) DO NOTHING`)
	e.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		e.schemaID, e.schemaSlug, `{"url_pattern":"/{slug}"}`)
	e.srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e.srv.renderErr != nil {
		t.Fatal(e.srv.renderErr)
	}
	e.ts = httptest.NewServer(e.srv.router)
	t.Cleanup(e.ts.Close)
	if err := db.Pool().QueryRow(ctx, `SELECT coalesce(max(id),0) FROM hatamoto_admin_audit`).Scan(&e.baseline); err != nil {
		t.Fatal(err)
	}

	hash, err := auth.HashPassword(entAuditPassword)
	if err != nil {
		t.Fatal(err)
	}
	e.owner, err = e.srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "ea-" + e.sfx + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, e.srv, e.owner)
	e.token, err = e.srv.authService.IssueSession(ctx, e.owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	e.secrets = append(e.secrets, e.token, entAuditPassword)
	t.Cleanup(e.cleanup)
	return e
}

func (e *entAuditEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Pool().Exec(e.ctx, q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *entAuditEnv) cleanup() {
	c := context.Background()
	p := e.srv.db.Pool()
	_, _ = p.Exec(c, `DELETE FROM hatamoto_admin_audit WHERE id > $1 AND (actor LIKE '%'||$2||'%' OR target = ANY($3) OR actor = ANY($4) OR detail::text LIKE '%'||$2||'%')`,
		e.baseline, e.sfx, e.targets, e.actors)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+e.sfx)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, e.owner.ID)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+e.sfx+"%")
	_, _ = p.Exec(c, `DELETE FROM hatamoto_terms WHERE name LIKE $1`, "%"+e.sfx)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_menus WHERE slug LIKE $1`, "%"+e.sfx)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_media WHERE original_filename LIKE $1`, "%"+e.sfx+"%")
	_, _ = p.Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1 OR slug LIKE $2`, e.schemaID, "%"+e.sfx)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, e.schemaID)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_sessions WHERE user_id=$1`, e.owner.ID)
	_, _ = p.Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, e.owner.ID)
}

// key makes an API key of the owner; its plain text is registered as a secret.
func (e *entAuditEnv) key(name string, scopes ...auth.APIKeyScope) *auth.APIKeyWithSecret {
	e.t.Helper()
	k, err := e.srv.apiKeyService.Create(e.ctx, &e.owner.ID, &auth.CreateAPIKeyInput{Name: name + e.sfx, Scopes: scopes})
	if err != nil {
		e.t.Fatal(err)
	}
	e.secrets = append(e.secrets, k.PlainKey)
	return k
}

type entAuditRow struct {
	Actor, Kind, Target, Action string
	KeyID                       *uuid.UUID
	Detail                      map[string]any
}

// rows lists the audit rows written since the test began that match the filter.
func (e *entAuditEnv) rows(where string, args ...any) []entAuditRow {
	e.t.Helper()
	q := `SELECT actor, actor_kind, coalesce(target,''), action, actor_key_id, detail FROM hatamoto_admin_audit WHERE id > $1 AND ` + where + ` ORDER BY id`
	rs, err := e.srv.db.Pool().Query(e.ctx, q, append([]any{e.baseline}, args...)...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []entAuditRow
	for rs.Next() {
		var r entAuditRow
		var raw []byte
		if err := rs.Scan(&r.Actor, &r.Kind, &r.Target, &r.Action, &r.KeyID, &raw); err != nil {
			e.t.Fatal(err)
		}
		if err := jsonx.Unmarshal(raw, &r.Detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (e *entAuditEnv) forTarget(id string) []entAuditRow {
	e.t.Helper()
	e.targets = append(e.targets, id)
	return e.rows(`target = $2`, id)
}

func (e *entAuditEnv) one(action, target string) entAuditRow {
	e.t.Helper()
	e.targets = append(e.targets, target)
	rs := e.rows(`action = $2 AND target = $3`, action, target)
	if len(rs) != 1 {
		e.t.Fatalf("%s rows for %s = %+v, want exactly one", action, target, rs)
	}
	return rs[0]
}

func (e *entAuditEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.srv.db.Pool().QueryRow(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// noSecrets fails when any row written since the test began holds a secret of
// this test (a key, a password, a session token) or the given extra strings.
func (e *entAuditEnv) noSecrets(extra ...string) {
	e.t.Helper()
	for _, s := range append(append([]string{}, e.secrets...), extra...) {
		if n := e.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE id > $1 AND (detail::text || actor || coalesce(target,'')) LIKE '%'||$2||'%'`, e.baseline, s); n != 0 {
			e.t.Errorf("%d audit rows hold a secret (%.4s...)", n, s)
		}
	}
}

func (e *entAuditEnv) do(method, path string, body []byte, hdr map[string]string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func entAuditID(t *testing.T, raw []byte) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := jsonx.Unmarshal(raw, &out); err != nil || out.ID == "" {
		t.Fatalf("no id in %s: %v", raw, err)
	}
	return out.ID
}

func (e *entAuditEnv) restCreate(key, slug string) (int, []byte) {
	e.t.Helper()
	b, _ := jsonx.Marshal(map[string]any{"schema_id": e.schemaID, "name": "Note " + slug, "slug": slug, "attributes": map[string]any{}})
	return e.do(http.MethodPost, "/api/v1/entities", b, map[string]string{"X-API-Key": key})
}

func (e *entAuditEnv) bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + e.token}
}

func TestRESTEntityWriteWithKeyIsAudited(t *testing.T) {
	e := entAuditNew(t)
	k := e.key("rest", auth.ScopeWriteEntities, auth.ScopeReadEntities)
	code, raw := e.restCreate(k.PlainKey, "rest-"+e.sfx)
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %s", code, raw)
	}
	id := entAuditID(t, raw)
	if code, raw := e.do(http.MethodPatch, "/api/v1/entities/"+id+"/status", []byte(`{"status":"published"}`), map[string]string{"X-API-Key": k.PlainKey}); code != http.StatusOK {
		t.Fatalf("PATCH status = %d %s", code, raw)
	}
	for _, action := range []string{"entity.create", "entity.publish"} {
		r := e.one(action, id)
		if r.Kind != "api_key" || r.Actor != "api-key:"+k.Name || r.KeyID == nil || r.KeyID.String() != k.ID.String() || r.Detail["via"] != "api" {
			t.Errorf("%s row = %+v", action, r)
		}
	}
	e.noSecrets()
}

func TestRESTEntityWriteWithSessionIsAuditedAsUser(t *testing.T) {
	e := entAuditNew(t)
	b, _ := jsonx.Marshal(map[string]any{"schema_id": e.schemaID, "name": "Session note", "slug": "sess-" + e.sfx, "attributes": map[string]any{}})
	code, raw := e.do(http.MethodPost, "/api/v1/entities", b, e.bearer())
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %s", code, raw)
	}
	r := e.one("entity.create", entAuditID(t, raw))
	if r.Kind != "user" || r.Actor != e.owner.Email || r.KeyID != nil || r.Detail["via"] != "api" {
		t.Errorf("row = %+v", r)
	}
	e.noSecrets()
}

func entAuditMCP(t *testing.T, e *entAuditEnv, key string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
	sess, err := c.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{key}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func entAuditMCPCall(t *testing.T, e *entAuditEnv, sess *mcp.ClientSession, tool string, args any) map[string]any {
	t.Helper()
	res, err := sess.CallTool(e.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("%s: %s", tool, text)
	}
	var out map[string]any
	if err := jsonx.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: %v\n%s", tool, err, text)
	}
	return out
}

func TestMCPEntityWriteIsAudited(t *testing.T) {
	e := entAuditNew(t)
	k := e.key("mcp", auth.ScopeWriteEntities, auth.ScopeReadEntities)
	sess := entAuditMCP(t, e, k.PlainKey)
	out := entAuditMCPCall(t, e, sess, "entities_create", map[string]any{"schema": e.schemaSlug, "name": "MCP note", "slug": "mcp-" + e.sfx})
	id, _ := out["id"].(string)
	r := e.one("entity.create", id)
	if r.Kind != "api_key" || r.Actor != "api-key:"+k.Name || r.KeyID == nil || r.KeyID.String() != k.ID.String() || r.Detail["via"] != "mcp" {
		t.Errorf("row = %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM hatamoto_mcp_audit WHERE api_key_name = $1 AND tool = 'entities_create' AND ok`, k.Name); n != 1 {
		t.Errorf("mcp audit rows = %d, want 1", n)
	}
	e.noSecrets()
}

type entAuditRefuser struct{}

func (entAuditRefuser) Record(context.Context, pgx.Tx, auditstate.Event) error {
	return errors.New("refused")
}

func TestRESTEntityWriteRefusedBySink(t *testing.T) {
	e := entAuditNew(t)
	k := e.key("refuse", auth.ScopeWriteEntities, auth.ScopeReadEntities)
	auditstate.Register(entAuditRefuser{})
	t.Cleanup(auditstate.Reset)
	slug := "refused-" + e.sfx
	code, raw := e.restCreate(k.PlainKey, slug)
	if code < 500 {
		t.Fatalf("POST with a refusing sink = %d %s, want 5xx", code, raw)
	}
	if n := e.count(`SELECT count(*) FROM hatamoto_entities WHERE slug = $1`, slug); n != 0 {
		t.Errorf("the refused write left %d entities", n)
	}
}

func TestEntityWriteWithoutActorNotAudited(t *testing.T) {
	e := entAuditNew(t)
	ent, err := e.srv.entityService.CreateEntity(e.ctx, nil, &entity.CreateEntityInput{SchemaID: e.schemaID, Name: "No actor", Slug: "noactor-" + e.sfx, Attributes: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.entityService.UpdateEntityStatus(e.ctx, ent.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	if rs := e.forTarget(ent.ID.String()); len(rs) != 0 {
		t.Errorf("writes without an actor produced rows: %+v", rs)
	}
}

type entAuditSite struct {
	e    *entAuditEnv
	csrf string
}

func (s entAuditSite) post(path string, form url.Values) *httptest.ResponseRecorder {
	s.e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminReq(req, s.e.token, s.csrf)
	rec := httptest.NewRecorder()
	s.e.srv.router.ServeHTTP(rec, req)
	return rec
}

func (s entAuditSite) status(id uuid.UUID) string {
	s.e.t.Helper()
	var st string
	if err := s.e.srv.db.Pool().QueryRow(s.e.ctx, `SELECT status FROM hatamoto_entities WHERE id=$1`, id).Scan(&st); err != nil {
		s.e.t.Fatal(err)
	}
	return st
}

func TestAdminEntityWriteInTx(t *testing.T) {
	e := entAuditNew(t)
	site := entAuditSite{e: e, csrf: adminToken(t, e.srv.router, e.token)}
	ent, err := e.srv.entityService.CreateEntity(e.ctx, &e.owner.ID, &entity.CreateEntityInput{SchemaID: e.schemaID, Name: "Admin note", Slug: "admin-" + e.sfx, Attributes: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"Admin note renamed"}, "slug": {"admin-" + e.sfx}, "status": {"published"}}
	if rec := site.post("/admin/entities/"+ent.ID.String(), form); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	rs := e.forTarget(ent.ID.String())
	if len(rs) != 2 || rs[0].Action != "entity.update" || rs[1].Action != "entity.publish" {
		t.Fatalf("rows = %+v, want exactly entity.update + entity.publish", rs)
	}
	for _, r := range rs {
		if r.Kind != "user" || r.Actor != e.owner.Email {
			t.Errorf("row = %+v", r)
		}
	}

	// A refusing sink fails the save and leaves the entity as it was.
	auditstate.Register(entAuditRefuser{})
	t.Cleanup(auditstate.Reset)
	form.Set("name", "Refused rename")
	form.Set("status", "draft")
	if rec := site.post("/admin/entities/"+ent.ID.String(), form); rec.Code == http.StatusSeeOther {
		t.Fatalf("a refused save redirected: %d", rec.Code)
	}
	var name string
	if err := e.srv.db.Pool().QueryRow(e.ctx, `SELECT name FROM hatamoto_entities WHERE id=$1`, ent.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Admin note renamed" || site.status(ent.ID) != "published" {
		t.Errorf("entity after a refused save: name %q status %q", name, site.status(ent.ID))
	}
	e.noSecrets()
}

func TestEntAuditAutosaveWritesNoRow(t *testing.T) {
	e := entAuditNew(t)
	site := entAuditSite{e: e, csrf: adminToken(t, e.srv.router, e.token)}
	var postSchema uuid.UUID
	if err := e.srv.db.Pool().QueryRow(e.ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug='post'`).Scan(&postSchema); err != nil {
		t.Skipf("no post schema: %v", err)
	}
	id := uuid.New()
	e.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,'Draft post',$3,'draft','{"content":"<p>x</p>"}')`, id, postSchema, "autosave-"+e.sfx)
	rec := site.post("/admin/entities/"+id.String()+"/edit/autosave", url.Values{"version": {"1"}, "name": {"Typing"}, "content": {"<p>Typing</p>"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("autosave = %d %s", rec.Code, rec.Body.String())
	}
	if rs := e.forTarget(id.String()); len(rs) != 0 {
		t.Errorf("autosave wrote rows: %+v", rs)
	}
}

func TestScheduledPublishWiring(t *testing.T) {
	e := entAuditNew(t)
	id := uuid.New()
	e.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes) VALUES ($1,$2,'Due',$3,'scheduled',now() - interval '1 minute','{}')`, id, e.schemaID, "due-"+e.sfx)
	// The scheduler's worker is built inside New; this one is wired with the
	// same writer, so it shows what the production hook records and refuses.
	w := worker.NewPublishWorker(e.srv.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.SetAuditTx(e.srv.entityAuditTx)

	auditstate.Register(entAuditRefuser{})
	if _, err := w.Run(e.ctx); err == nil {
		t.Error("a refusing sink did not fail the run")
	}
	auditstate.Reset()
	if st := e.count(`SELECT count(*) FROM hatamoto_entities WHERE id=$1 AND status='scheduled'`, id); st != 1 {
		t.Fatal("a refused promotion changed the entity")
	}
	if _, err := w.Run(e.ctx); err != nil {
		t.Fatal(err)
	}
	r := e.one("entity.publish_scheduled", id.String())
	if r.Kind != "system" || r.Actor != "system (scheduler)" {
		t.Errorf("row = %+v", r)
	}
	if rs := e.forTarget(id.String()); len(rs) != 1 {
		t.Errorf("rows = %+v, want only the scheduler row", rs)
	}
}

func TestRESTContentWritesAudited(t *testing.T) {
	e := entAuditNew(t)
	bearer := e.bearer()
	post := func(path, body string) string {
		t.Helper()
		code, raw := e.do(http.MethodPost, path, []byte(body), bearer)
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", path, code, raw)
		}
		return entAuditID(t, raw)
	}
	menuID := post("/api/v1/menus", `{"name":"Menu `+e.sfx+`","slug":"menu-`+e.sfx+`"}`)
	rdID := post("/api/v1/redirects", `{"source_path":"/old-`+e.sfx+`?token=abc","target_url":"/new-`+e.sfx+`","status_code":301}`)
	termID := post("/api/v1/terms", `{"kind":"tag","name":"Tag `+e.sfx+`","slug":"tag-`+e.sfx+`"}`)
	for action, id := range map[string]string{"menu.created": menuID, "redirect.created": rdID, "term.created": termID} {
		r := e.one(action, id)
		if r.Kind != "user" || r.Actor != e.owner.Email || r.Detail["via"] != "api" {
			t.Errorf("%s row = %+v", action, r)
		}
	}
	if r := e.one("redirect.created", rdID); strings.Contains(r.Detail["source"].(string), "token") {
		t.Errorf("a query string reached the audit row: %v", r.Detail)
	}

	// Media upload goes through a key with write:media.
	k := e.key("media", auth.ScopeWriteMedia)
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "dot-"+e.sfx+".png")
	_, _ = fw.Write(png)
	_ = mw.Close()
	code, raw := e.do(http.MethodPost, "/api/v1/media/upload", body.Bytes(), map[string]string{"X-API-Key": k.PlainKey, "Content-Type": mw.FormDataContentType()})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("upload = %d %s", code, raw)
	}
	mr := e.one("media.uploaded", entAuditID(t, raw))
	if mr.Kind != "api_key" || mr.Detail["via"] != "api" {
		t.Errorf("media row = %+v", mr)
	}

	// The admin writes its own term row; the service hook adds none.
	site := entAuditSite{e: e, csrf: adminToken(t, e.srv.router, e.token)}
	name := "Admin Tag " + e.sfx
	if rec := site.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"tag"}, "name": {name}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin term = %d %s", rec.Code, rec.Body.String())
	}
	var adminTerm string
	if err := e.srv.db.Pool().QueryRow(e.ctx, `SELECT id::text FROM hatamoto_terms WHERE name=$1`, name).Scan(&adminTerm); err != nil {
		t.Fatal(err)
	}
	if r := e.one("term.created", adminTerm); r.Kind != "user" || r.Detail["via"] != nil {
		t.Errorf("admin term row = %+v", r)
	}
	e.noSecrets()
}

func TestMCPContentWriteAudited(t *testing.T) {
	e := entAuditNew(t)
	k := e.key("mcpc", auth.ScopeWriteRedirects)
	sess := entAuditMCP(t, e, k.PlainKey)
	out := entAuditMCPCall(t, e, sess, "redirects_create", map[string]any{"source_path": "/mcp-old-" + e.sfx, "target_url": "/mcp-new-" + e.sfx})
	id, _ := out["id"].(string)
	r := e.one("redirect.created", id)
	if r.Kind != "api_key" || r.Actor != "api-key:"+k.Name || r.KeyID == nil || r.Detail["via"] != "mcp" {
		t.Errorf("row = %+v", r)
	}
	e.noSecrets()
}

func TestEntAuditSignInRows(t *testing.T) {
	e := entAuditNew(t)
	login := func(email, pw string) int {
		t.Helper()
		b, _ := jsonx.Marshal(map[string]string{"email": email, "password": pw})
		code, _ := e.do(http.MethodPost, "/api/v1/auth/login", b, nil)
		return code
	}
	if code := login(e.owner.Email, entAuditPassword); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	typed := "ghost-" + e.sfx + "@example.test"
	if code := login(typed, "nope-nope-nope-nope"); code == http.StatusOK {
		t.Fatal("unknown user signed in")
	}
	if code := login(e.owner.Email, "wrong-wrong-wrong-1"); code == http.StatusOK {
		t.Fatal("wrong password signed in")
	}
	in := e.rows(`action = 'session.sign_in' AND actor = $2`, e.owner.Email)
	if len(in) != 1 || in[0].Kind != "user" || in[0].Detail["method"] != "password" {
		t.Fatalf("sign-in rows = %+v", in)
	}
	hash := auditlog.NewEmailHasher(e.srv.cfg.Secret)(typed)
	e.actors = append(e.actors, hash, auditlog.NewEmailHasher(e.srv.cfg.Secret)(e.owner.Email))
	failed := e.rows(`action = 'session.sign_in_failed' AND (actor = $2 OR actor = $3)`, hash, auditlog.NewEmailHasher(e.srv.cfg.Secret)(e.owner.Email))
	if len(failed) != 2 {
		t.Fatalf("failed sign-in rows = %+v, want 2", failed)
	}
	for _, r := range failed {
		if !strings.HasPrefix(r.Actor, "email-hash:") {
			t.Errorf("failed sign-in actor = %q", r.Actor)
		}
	}
	// The typed address of a failed sign-in is never stored in clear.
	if n := e.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE id > $1 AND action = 'session.sign_in_failed' AND (detail::text || actor || coalesce(target,'')) LIKE '%@%'`, e.baseline); n != 0 {
		t.Errorf("%d failed sign-in rows hold an address", n)
	}
	e.noSecrets(typed, "wrong-wrong-wrong-1")
}
