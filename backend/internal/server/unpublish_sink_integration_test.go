//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/worker"
)

type unpublishRig struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	srv    *Server
	probe  *renderProbe
	schema uuid.UUID
}

func newUnpublishRig(t *testing.T) *unpublishRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &unpublishRig{t: t, ctx: ctx, db: db, schema: uuid.New()}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings)
		VALUES ($1,'UPage','unpub-page','{"type":"object"}','{"url_pattern":"/{slug}"}')`, r.schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, r.schema)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, r.schema)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_redirects WHERE source_path LIKE '/unpub-%'`)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_admin_audit WHERE actor = $1`, schedulerActor)
	})
	repo := entity.NewPostgresEntityRepository(db)
	svc := entity.NewService(entity.NewPostgresSchemaRepository(db), repo, logger)
	svc.SetUnpublishStore(entity.NewUnpublishStore(db))
	r.probe = newRenderProbe(logger)
	r.srv = &Server{db: db, entityService: svc, entityRepo: repo, logger: logger,
		redirectService: redirect.NewService(db, logger),
		infra:           &Infrastructure{Cache: r.probe.c, CacheInvalidator: cache.NewCacheInvalidator(r.probe.c, logger)}}
	t.Cleanup(r.srv.redirectService.Close)
	svc.SetLiveObserver(r.srv.onEntitiesLive)
	return r
}

func (r *unpublishRig) published(slug string) *entity.Entity {
	r.t.Helper()
	e, err := r.srv.entityService.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{
		SchemaID: r.schema, Name: slug, Slug: slug, Locale: r.srv.siteLanguage(r.ctx), Attributes: json.RawMessage(`{}`)})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		r.t.Fatal(err)
	}
	return e
}

func (r *unpublishRig) due(id uuid.UUID, target string) {
	r.t.Helper()
	if err := r.srv.entityService.SetUnpublish(r.ctx, id, time.Now().Add(-time.Minute), target); err != nil {
		r.t.Fatal(err)
	}
}

func (r *unpublishRig) run() int {
	r.t.Helper()
	n, err := worker.NewUnpublishWorker(r.db, r.srv.logger, unpublishSink{s: r.srv}).Run(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *unpublishRig) status(id uuid.UUID) entity.EntityStatus {
	e, err := r.srv.entityRepo.GetByID(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return e.Status
}

func (r *unpublishRig) redirectRow(path string) (target string, code int, active, found bool) {
	err := r.db.Pool().QueryRow(r.ctx,
		`SELECT target_url, status_code, is_active FROM hatamoto_redirects WHERE source_path = $1`, path).Scan(&target, &code, &active)
	return target, code, active, err == nil
}

func TestScheduledUnpublishWithRedirect(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-a")
	r.due(e.ID, "/elsewhere")
	r.probe.prime()

	if n := r.run(); n != 1 {
		t.Fatalf("worker handled %d, want 1", n)
	}
	if got := r.status(e.ID); got != entity.StatusDraft {
		t.Fatalf("status = %s, want draft", got)
	}
	if !r.probe.cleared() {
		t.Error("page cache was not cleared")
	}
	target, code, active, found := r.redirectRow("/unpub-a")
	if !found || target != "/elsewhere" || code != 302 || !active {
		t.Fatalf("redirect = %q %d active=%v found=%v", target, code, active, found)
	}
	var n int
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_admin_audit
		WHERE actor = $1 AND action = $2 AND target = $3 AND detail->>'redirect' = '/elsewhere'`,
		schedulerActor, auditEntityUnpublishScheduled, e.ID.String()).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}

	// A second tick finds nothing due.
	if n := r.run(); n != 0 {
		t.Errorf("second tick handled %d", n)
	}

	// Publishing again removes the redirect.
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found := r.redirectRow("/unpub-a"); found {
		t.Error("redirect still present after republish")
	}
}

// The same page unpublished a second time (a seasonal page) gets its
// redirect again: the first cycle's redirect must not block the path.
func TestSecondUnpublishCycleGetsItsRedirect(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-f")
	for cycle, target := range []string{"/first", "/second"} {
		r.due(e.ID, target)
		if n := r.run(); n != 1 {
			t.Fatalf("cycle %d: handled %d", cycle, n)
		}
		got, code, active, found := r.redirectRow("/unpub-f")
		if !found || got != target || code != 302 || !active {
			t.Fatalf("cycle %d: redirect = %q %d active=%v found=%v", cycle, got, code, active, found)
		}
		if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScheduledUnpublishWithoutTargetLeavesNoRedirect(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-b")
	r.due(e.ID, "")
	if n := r.run(); n != 1 {
		t.Fatalf("handled %d", n)
	}
	if r.status(e.ID) != entity.StatusDraft {
		t.Error("not unpublished")
	}
	if _, _, _, found := r.redirectRow("/unpub-b"); found {
		t.Error("a redirect was created without a target")
	}
	if u, _ := r.srv.entityService.UnpublishFor(r.ctx, e.ID); u != nil {
		t.Error("schedule row not dropped")
	}
}

func TestScheduledUnpublishKeepsOwnerRedirectAndSkipsDrafts(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-c")
	if _, err := r.srv.redirectService.CreateRedirect(r.ctx, &redirect.CreateRedirectInput{SourcePath: "/unpub-c", TargetURL: "/owner", StatusCode: 301}); err != nil {
		t.Fatal(err)
	}
	r.due(e.ID, "/other")
	r.run()
	if target, code, _, _ := r.redirectRow("/unpub-c"); target != "/owner" || code != 301 {
		t.Errorf("owner rule overwritten: %q %d", target, code)
	}
	if r.status(e.ID) != entity.StatusDraft {
		t.Error("entity still published")
	}

	// A draft with a due schedule is not touched: the row is just dropped.
	d, err := r.srv.entityService.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{
		SchemaID: r.schema, Name: "d", Slug: "unpub-d", Locale: r.srv.siteLanguage(r.ctx), Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.due(d.ID, "/x")
	r.run()
	if _, _, _, found := r.redirectRow("/unpub-d"); found {
		t.Error("redirect created for a draft")
	}
}

// A fired unpublish must not leave a pending schedule behind: republishing
// from the editor re-posts nothing, and the next tick must not take the page
// down again.
func TestFiredUnpublishLeavesNoPendingSchedule(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-e")
	r.due(e.ID, "/elsewhere")
	if n := r.run(); n != 1 {
		t.Fatalf("handled %d", n)
	}
	if u, _ := r.srv.entityService.UnpublishFor(r.ctx, e.ID); u != nil {
		t.Fatalf("fired schedule still visible: %+v", u)
	}
	if m, _ := r.srv.entityService.UnpublishMap(r.ctx, []uuid.UUID{e.ID}); len(m) != 0 {
		t.Fatalf("fired schedule in Map: %+v", m)
	}

	// Setting a new date on the draft is a normal pending schedule, and the
	// next publish does not swallow it silently.
	if err := r.srv.entityService.SetUnpublish(r.ctx, e.ID, time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if err := r.srv.entityService.ClearUnpublish(r.ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := r.srv.entityService.UnpublishFor(r.ctx, e.ID); u != nil {
		t.Fatal("Clear did nothing")
	}

	// Republish, then a tick: the page stays published.
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	r.run()
	if r.status(e.ID) != entity.StatusPublished {
		t.Fatal("page was unpublished again after republish")
	}
}

// Two ticks racing for one due row act once: one status write, one audit row.
func TestConcurrentUnpublishTicksActOnce(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-g")
	r.due(e.ID, "/elsewhere")
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := (unpublishSink{s: r.srv}).Unpublish(r.ctx, e.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_admin_audit
		WHERE actor = $1 AND action = $2 AND target = $3`,
		schedulerActor, auditEntityUnpublishScheduled, e.ID.String()).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}
}

// An owner who pushes the date back after the worker selected the row wins:
// the sink re-checks the due time and leaves the page and the new date alone.
func TestPostponedUnpublishIsNotCarriedOut(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-h")
	if err := r.srv.entityService.SetUnpublish(r.ctx, e.ID, time.Now().Add(time.Hour), "/x"); err != nil {
		t.Fatal(err)
	}
	if err := (unpublishSink{s: r.srv}).Unpublish(r.ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if r.status(e.ID) != entity.StatusPublished {
		t.Error("a postponed page was unpublished")
	}
	if u, _ := r.srv.entityService.UnpublishFor(r.ctx, e.ID); u == nil {
		t.Error("the new schedule was deleted")
	}
}

// cancelOnGet cancels the tick context as soon as the sink reads the entity,
// which is after the claim and before the status write.
type cancelOnGet struct {
	entity.EntityRepository
	cancel context.CancelFunc
}

func (c cancelOnGet) GetByID(ctx context.Context, id uuid.UUID) (*entity.Entity, error) {
	c.cancel()
	return c.EntityRepository.GetByID(ctx, id)
}

// A tick that ends right after the claim must not lose the schedule.
func TestCancelledTickRestoresClaimedSchedule(t *testing.T) {
	r := newUnpublishRig(t)
	e := r.published("unpub-i")
	r.due(e.ID, "/elsewhere")
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	r.srv.entityRepo = cancelOnGet{EntityRepository: r.srv.entityRepo, cancel: cancel}
	if err := (unpublishSink{s: r.srv}).Unpublish(ctx, e.ID); err == nil {
		t.Fatal("expected the cancelled tick to fail")
	}
	if u, _ := r.srv.entityService.UnpublishFor(r.ctx, e.ID); u == nil {
		t.Fatal("claimed schedule was lost")
	}
}
