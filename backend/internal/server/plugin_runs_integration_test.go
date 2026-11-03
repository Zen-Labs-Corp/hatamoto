//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/sdk"
)

// The owner starts a plugin run through the whole server: session, owner
// role, CSRF, the admin route, the runtime and the changeset engine. The run
// ends with a staged changeset and nothing live changed.
func TestPluginRunStartIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "runs-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Runs IT")
	t.Setenv("PUBLIC_BASE_URL", "https://runs-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
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
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}
	if srv.plugins == nil {
		t.Skip("no plugin runtime")
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	schemaID, schema := uuid.New(), "run-"+sfx
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`, schemaID, schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_runs WHERE plugin = 'runner'`)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "/"+sfx+"%")
	})

	hash, _ := auth.HashPassword("a-long-enough-password")
	session := func(role user.Role) string {
		u, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: string(role) + "-" + sfx + "@example.test", Role: role, Name: string(role)}, hash)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, u.ID) })
		completeProfile(t, srv, u)
		tok, err := srv.authService.IssueSession(ctx, u.ID, "it", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	ownerTok, editorTok := session(user.RoleOwner), session(user.RoleEditor)
	post := func(tok, csrf string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/admin/plugins/runner/runs/import/start", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		adminReq(req, tok, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	ownerCSRF, editorCSRF := adminToken(t, srv.router, ownerTok), adminToken(t, srv.router, editorTok)

	st := srv.plugins.Load(ctx, sdk.Manifest{Name: "runner", Version: "0.1.0", SDK: "^0.17", Runs: []sdk.Run{{
		Name: "import", Title: "Import", Stages: []string{sdk.RunPhaseFetch, sdk.RunPhaseTransform},
		Inputs: []sdk.RunInput{{Name: "schema", Label: "Schema", Required: true}, {Name: "tag", Label: "Tag"}, {Name: "mode", Label: "Mode"}},
	}}}, plugintest.Build(t, "runner"))
	if !st.Loaded {
		t.Fatalf("runner not loaded: %s", st.Error)
	}
	form := func() url.Values { return url.Values{"input_schema": {schema}, "input_tag": {sfx}} }
	store := &plugrun.Store{DB: db}

	// Refused: an editor, and an owner without the token. No run row appears.
	if rec := post(editorTok, editorCSRF, form()); rec.Code != http.StatusForbidden {
		t.Fatalf("editor: %d", rec.Code)
	}
	if rec := post(ownerTok, "", form()); rec.Code != http.StatusForbidden {
		t.Fatalf("owner without CSRF: %d", rec.Code)
	}
	if n := count(`SELECT count(*) FROM hatamoto_runs WHERE plugin = 'runner'`); n != 0 {
		t.Fatalf("%d runs after refused starts", n)
	}
	// Refused: a missing required input.
	if rec := post(ownerTok, ownerCSRF, url.Values{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing input: %d", rec.Code)
	}

	// One active run per plugin: a run left running blocks the start with 409.
	blocker, err := store.Create(ctx, plugrun.Run{Plugin: "runner", Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(ownerTok, ownerCSRF, form()); rec.Code != http.StatusConflict {
		t.Fatalf("second start: %d", rec.Code)
	}
	if err := store.Fail(ctx, blocker.ID, "done"); err != nil {
		t.Fatal(err)
	}

	before := count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
	rec := post(ownerTok, ownerCSRF, form())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("owner start: %d %s", rec.Code, rec.Body.String())
	}
	srv.plugins.WaitRuns()
	var id uuid.UUID
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_runs WHERE plugin = 'runner' AND state <> 'failed'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	run, _ := store.Get(ctx, id)
	if run.State != plugrun.StateFinished || run.ChangesetID == nil {
		t.Fatalf("run %s: %s", run.State, run.Reason)
	}
	if n := count(`SELECT count(*) FROM hatamoto_changesets WHERE id = $1 AND state = 'staged'`, *run.ChangesetID); n != 1 {
		t.Fatal("changeset is not staged")
	}
	if n := count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, *run.ChangesetID); n != 3 {
		t.Fatalf("staged items = %d, want 3", n)
	}
	if after := count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, schemaID); after != before {
		t.Fatalf("live entities changed: %d -> %d", before, after)
	}
}
