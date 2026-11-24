//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// A plugin-created entry goes through the same service as an editor's: it is
// served at its URL pattern, listed in the sitemap, takes comments, writes one
// audit row, and leaves 404 with its comments gone after the plugin deletes it.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run TestPluginEntityPublicPage -v
func TestPluginEntityPublicPage_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "pw-it")
	t.Setenv("HATAMOTO_SITE_NAME", "PW IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "PW IT")
	t.Setenv("PUBLIC_BASE_URL", "https://pw-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
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

	schemaID, schema := uuid.New(), "story-"+uuid.NewString()[:6]
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings, origin_plugin)
		VALUES ($1, $2, $2, '{"type":"object","properties":{"url":{"type":"string"}}}', $3, 'fixture')`,
		schemaID, schema, `{"url_pattern":"/item/{slug}"}`)
	var prevName string
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name FROM hatamoto_site_config`).Scan(&prevName) == nil
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('PW IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_admin_audit WHERE actor = 'plugin:fixture' AND detail->>'schema' = $1`, schema)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_comment_settings`)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	set := comment.Defaults()
	set.Enabled = true
	if err := srv.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}

	grants := pluginhost.NewGrantStore(nil)
	if err := grants.Set(ctx, "fixture", []sdk.Permission{sdk.PermEntitiesWriteOwn}); err != nil {
		t.Fatal(err)
	}
	manifest := sdk.Manifest{Name: "fixture", SDK: "^0.27", Permissions: []string{sdk.PermEntitiesWriteOwn},
		EntitySchema: schema, EntityFields: []string{"url"}}
	api := pluginhost.NewAPI(pluginhost.Services{Entities: srv.entityService, EntityWrites: srv.entityService},
		grants, func(string) (sdk.Manifest, bool) { return manifest, true }, nil)
	call := func(fn string, args any) sdk.CallResponse {
		t.Helper()
		raw, _ := jsonx.MarshalPinned(args)
		in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
		var out sdk.CallResponse
		if err := jsonx.UnmarshalPinned(api.Call(ctx, "fixture", in), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}

	out := call(sdk.FnEntitiesCreate, map[string]any{"name": "First story", "slug": "1001", "attributes": map[string]any{"url": "https://example.test/a"}})
	if !out.OK {
		t.Fatalf("create: %+v", out.Error)
	}
	var dto struct{ ID string }
	if err := jsonx.UnmarshalPinned(out.Result, &dto); err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(dto.ID)

	if code, body := get("/item/1001"); code != http.StatusOK || !strings.Contains(body, "First story") {
		t.Fatalf("GET /item/1001 = %d\n%s", code, body)
	}
	if !sitemapsList(t, get, "/item/1001") {
		t.Fatal("the new entry is not in the sitemap")
	}

	if _, err := srv.comments.Store(ctx, comment.Draft{
		EntityID: id, AuthorName: "Visitor", BodyRaw: "Nice.", BodyHTML: commentfmtHTML(t, "Nice."),
		SpamVerdict: "allow", Status: comment.StatusApproved,
	}); err != nil {
		t.Fatalf("comment on the plugin's entry: %v", err)
	}

	auditRow := func(action string) (actor, kind string, n int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, `SELECT count(*), coalesce(max(actor), ''), coalesce(max(actor_kind), '')
			FROM hatamoto_admin_audit WHERE action = $1 AND target = $2`, action, id.String()).Scan(&n, &actor, &kind); err != nil {
			t.Fatal(err)
		}
		return actor, kind, n
	}
	if actor, kind, n := auditRow("plugin.entity.create"); n != 1 || actor != "plugin:fixture" || kind != "plugin" {
		t.Fatalf("create audit rows = %d (%q, %q)", n, actor, kind)
	}
	if _, _, n := auditRow("entity.create"); n != 0 {
		t.Fatalf("%d repository audit rows for a plugin write", n)
	}

	if out := call(sdk.FnEntitiesDelete, map[string]any{"id": dto.ID}); !out.OK {
		t.Fatalf("delete: %+v", out.Error)
	}
	if code, _ := get("/item/1001"); code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", code)
	}
	var comments int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_comments WHERE entity_id = $1`, id).Scan(&comments); err != nil || comments != 0 {
		t.Fatalf("comments left = %d, %v", comments, err)
	}
	if _, _, n := auditRow("plugin.entity.delete"); n != 1 {
		t.Fatalf("delete audit rows = %d", n)
	}
}

func commentfmtHTML(t *testing.T, raw string) string {
	t.Helper()
	res, err := commentfmt.Render(raw)
	if err != nil {
		t.Fatal(err)
	}
	return res.HTML
}

var sitemapLoc = regexp.MustCompile(`<loc>[^<]*(/sitemap[^<]*\.xml)</loc>`)

// sitemapsList reports whether the index or any child sitemap lists path.
func sitemapsList(t *testing.T, get func(string) (int, string), path string) bool {
	t.Helper()
	code, index := get("/sitemap.xml")
	if code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml = %d", code)
	}
	if strings.Contains(index, path+"</loc>") {
		return true
	}
	for _, m := range sitemapLoc.FindAllStringSubmatch(index, -1) {
		if code, body := get(m[1]); code == http.StatusOK && strings.Contains(body, path+"</loc>") {
			return true
		}
	}
	return false
}
