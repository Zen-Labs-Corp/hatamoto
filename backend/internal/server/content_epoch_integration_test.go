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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

func must(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func eventually(t *testing.T, within time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(msg)
}

// A page published by another process (the CLI builds a bare entity.Service:
// no hook sink, its own pool) must show up in the header of a "/" the running
// server already cached, within the poll interval and without a purge call:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run ContentEpoch
func TestContentEpoch_CLIWriteRefreshesCachedHome_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "ce-it")
	t.Setenv("HATAMOTO_SITE_NAME", "CE IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "CE IT")
	t.Setenv("PUBLIC_BASE_URL", "https://ce-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h") // a stale page would live for a day
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	cfg, err := config.Load()
	must(t, err, "setup")

	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	must(t, err, "setup")
	t.Cleanup(db.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, db, log)

	sfx := uuid.NewString()[:6]
	var pageSchemaID, homeID uuid.UUID
	must(t, db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&pageSchemaID), "setup")
	must(t, db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,'Home',$2,'published',now(),'{"page_sections":[]}'::jsonb) RETURNING id`, pageSchemaID, "home-"+sfx).Scan(&homeID), "setup")
	var prevHome *uuid.UUID
	var prevName string
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevName, &prevHome) == nil
	_, err = db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('CE IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, homeID)
	must(t, err, "setup")
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET homepage_entity_id=$1`, prevHome)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE slug LIKE $1`, "%"+sfx)
	})

	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec
	}
	key := cache.RenderedPageKey(srv.renderEngine.Version(), "/")

	// The poller exactly as Start wires it; it seeds on its first read.
	pollCtx, stopPoll := context.WithCancel(ctx)
	pollDone := make(chan struct{})
	go func() { defer close(pollDone); srv.runContentEpochPoller(pollCtx) }()
	t.Cleanup(func() { stopPoll(); <-pollDone })
	every := srv.contentEpochEvery()
	if every != time.Second {
		t.Fatalf("dev poll interval = %v, want 1s", every)
	}
	time.Sleep(every / 4) // let the seed read happen before the write

	navLabel := "Themes " + sfx
	rec := get()
	if rec.Code != 200 {
		t.Fatalf("GET /: %d %.300s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), navLabel) {
		t.Fatalf("header already lists %q before it exists", navLabel)
	}
	_, err = srv.infra.Cache.Get(ctx, key)
	must(t, err, "/ was not cached")
	before := rec.Result().Header.Clone()
	if !strings.Contains(before.Get("Cache-Control"), "max-age") {
		t.Fatalf("unexpected cache-control: %v", before)
	}

	// The "CLI": its own pool and a bare service, nothing wired to the server.
	cliDB, err := database.New(ctx, cfg.Database)
	must(t, err, "setup")
	t.Cleanup(cliDB.Close)
	cli := entity.NewService(entity.NewPostgresSchemaRepository(cliDB), entity.NewPostgresEntityRepository(cliDB), log)
	created, err := cli.CreateEntity(ctx, nil, &entity.CreateEntityInput{
		SchemaID: pageSchemaID, Name: navLabel, Slug: "themes-" + sfx, Attributes: json.RawMessage(`{"page_sections":[]}`)})
	must(t, err, "setup")
	_, err = cli.UpdateEntityStatus(ctx, created.ID, nil, entity.StatusPublished)
	must(t, err, "setup")

	// Still the old cached copy right after the write is allowed; it must not
	// stay that way for longer than a few polls.
	eventually(t, 3*every+2*time.Second, func() bool {
		return strings.Contains(get().Body.String(), navLabel)
	}, "cached / never showed the page published by the other process")

	// Same cache lifetime and headers as before: only the content changed.
	after := get().Result().Header
	if b, a := before.Get("Cache-Control"), after.Get("Cache-Control"); a != b {
		t.Errorf("cache-control changed: %q -> %q", b, a)
	}
}

func newChangeReader(t *testing.T) (*contentChangeReader, *database.DB, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "ce-it")
	t.Setenv("HATAMOTO_SITE_NAME", "CE IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "CE IT")
	t.Setenv("PUBLIC_BASE_URL", "https://ce-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	cfg, err := config.Load()
	must(t, err, "setup")
	db, err := database.New(context.Background(), cfg.Database)
	must(t, err, "setup")
	t.Cleanup(db.Close)
	var schemaID uuid.UUID
	must(t, db.Pool().QueryRow(context.Background(), `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&schemaID), "setup")
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE slug LIKE 'cc-%'`)
	})
	r := &contentChangeReader{db: db.Pool(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	seeded, err := r.Changed(context.Background())
	must(t, err, "seed")
	if seeded {
		t.Fatal("the seeding call reported a change")
	}
	return r, db, schemaID
}

func mustChanged(t *testing.T, r *contentChangeReader, want bool, what string) {
	t.Helper()
	got, err := r.Changed(context.Background())
	must(t, err, what)
	if got != want {
		t.Fatalf("%s: changed = %v, want %v", what, got, want)
	}
}

// Two transactions that commit in the reverse order of their log ids: a poll
// between the commits sees one change, the next sees the other. A "max id
// grew" check would miss the second.
func TestContentChangeReader_OutOfOrderCommitsAreBothSeen_Integration(t *testing.T) {
	ctx := context.Background()
	r, db, schemaID := newChangeReader(t)
	sfx := uuid.NewString()[:6]
	ins := func(tx pgx.Tx, slug string) {
		_, err := tx.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug) VALUES ($1,$2,$2)`, schemaID, slug+sfx)
		must(t, err, "insert")
	}
	a, err := db.Pool().Begin(ctx) // gets the lower log id
	must(t, err, "begin a")
	ins(a, "cc-a")
	b, err := db.Pool().Begin(ctx) // the higher one
	must(t, err, "begin b")
	ins(b, "cc-b")

	mustChanged(t, r, false, "poll with both transactions open")
	must(t, b.Commit(ctx), "commit b first")
	mustChanged(t, r, true, "poll after the later transaction committed")
	mustChanged(t, r, false, "idle poll")
	must(t, a.Commit(ctx), "commit a last")
	mustChanged(t, r, true, "poll after the earlier transaction committed late")
	mustChanged(t, r, false, "idle poll after both")
}

// A rolled-back transaction and zero-row statements never register.
func TestContentChangeReader_RollbackAndZeroRowsAreSilent_Integration(t *testing.T) {
	ctx := context.Background()
	r, db, schemaID := newChangeReader(t)
	sfx := uuid.NewString()[:6]

	tx, err := db.Pool().Begin(ctx)
	must(t, err, "begin")
	_, err = tx.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug) VALUES ($1,'cc-rb','cc-rb-'||$2)`, schemaID, sfx)
	must(t, err, "insert")
	mustChanged(t, r, false, "poll while the transaction is open")
	must(t, tx.Rollback(ctx), "rollback")
	mustChanged(t, r, false, "poll after rollback")

	// The scheduled-publish worker's UPDATE with nothing due.
	_, err = db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status='published' WHERE status='scheduled' AND published_at <= now() - interval '100 years'`)
	must(t, err, "zero-row update")
	mustChanged(t, r, false, "poll after a zero-row update")

	_, err = db.Pool().Exec(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug) VALUES ($1,'cc-ok','cc-ok-'||$2)`, schemaID, sfx)
	must(t, err, "committed insert")
	mustChanged(t, r, true, "poll after a committed write")
	mustChanged(t, r, false, "idle poll")
}
