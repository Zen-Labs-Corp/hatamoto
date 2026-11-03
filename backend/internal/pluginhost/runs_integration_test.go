//go:build integration

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/sdk"
)

const runnerPlugin = "runner"

// driverRig is a runtime with the runner plugin loaded over a real run store
// and changeset engine.
type driverRig struct {
	t      *testing.T
	rt     *Runtime
	db     *database.DB
	runs   *plugrun.Store
	eng    *changeset.Engine
	schema string
	tag    string
}

func newDriverRig(t *testing.T) *driverRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &driverRig{t: t, db: db, tag: strings.ReplaceAll(uuid.NewString(), "-", "")[:8]}

	schemas := entity.NewPostgresSchemaRepository(db)
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: "run " + r.tag, Slug: "run-" + r.tag,
		Schema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.schema = sc.Slug
	ents := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), log)
	r.eng = &changeset.Engine{DB: db, Logger: log, Writers: map[changeset.Kind]changeset.KindWriter{
		changeset.KindEntity:   changeset.EntityWriter{Svc: ents},
		changeset.KindRedirect: changeset.RedirectWriter{Svc: redirect.NewService(db, log)},
	}}
	r.runs = &plugrun.Store{DB: db}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_runs WHERE plugin = $1`, runnerPlugin)
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_ = schemas.Delete(bg, sc.ID)
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "/"+r.tag+"%")
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_changeset_source_keys WHERE source_key LIKE $1`, r.tag+"%")
	})

	rt, err := NewRuntime(log, hooks.New(log), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	rt.Configure(Config{Services: Services{Runs: r.runs, Changesets: r.eng}})
	r.rt = rt
	st := rt.Load(ctx, sdk.Manifest{Name: runnerPlugin, Version: "0.1.0", SDK: "^0.17", Runs: []sdk.Run{{
		Name: "import", Title: "Import", Stages: []string{sdk.RunPhaseFetch, sdk.RunPhaseTransform},
		Inputs: []sdk.RunInput{{Name: "schema", Label: "Schema"}, {Name: "tag", Label: "Tag"}, {Name: "mode", Label: "Mode"}},
	}}}, plugintest.Build(t, "runner"))
	if !st.Loaded {
		t.Fatalf("runner not loaded: %s", st.Error)
	}
	return r
}

func (r *driverRig) start(mode string) (uuid.UUID, error) {
	return r.rt.StartRun(context.Background(), runnerPlugin, "import", StartRunInput{
		Inputs: map[string]string{"schema": r.schema, "tag": r.tag, "mode": mode}})
}

// finished starts a run, waits for its driver and returns the stored run.
func (r *driverRig) finished(mode string) plugrun.Run {
	r.t.Helper()
	id, err := r.start(mode)
	if err != nil {
		r.t.Fatal(err)
	}
	r.rt.WaitRuns()
	run, err := r.runs.Get(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return run
}

func (r *driverRig) count(q string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.Pool().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *driverRig) liveObjects() (entities, redirects int) {
	return r.count(`SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE s.slug = $1`, r.schema),
		r.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path LIKE $1`, "/"+r.tag+"%")
}

func (r *driverRig) changesetState(id int64) string {
	r.t.Helper()
	var s string
	if err := r.db.Pool().QueryRow(context.Background(), `SELECT state FROM hatamoto_changesets WHERE id = $1`, id).Scan(&s); err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *driverRig) items(csID int64) int {
	return r.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, csID)
}

// The owner starts a run: the fetch phase runs two steps (the second resumed
// from the checkpoint), then the transform phase stages the items, and the
// changeset waits staged while nothing live has changed.
func TestRunFetchThenTransform(t *testing.T) {
	r := newDriverRig(t)
	e0, d0 := r.liveObjects()
	run := r.finished("")
	if run.State != plugrun.StateFinished || run.Phase != plugrun.PhaseTransform {
		t.Fatalf("run %s in phase %s: %s", run.State, run.Phase, run.Reason)
	}
	if run.Steps != 3 { // fetch, fetch resumed from page-2, transform
		t.Fatalf("steps = %d, want 3", run.Steps)
	}
	if string(run.Summary) != `{"records": 2}` && string(run.Summary) != `{"records":2}` {
		t.Fatalf("summary %s", run.Summary)
	}
	if run.ChangesetID == nil {
		t.Fatal("run has no changeset")
	}
	if got := r.changesetState(*run.ChangesetID); got != changeset.StateStaged {
		t.Fatalf("changeset %s, want staged", got)
	}
	if n := r.items(*run.ChangesetID); n != 3 { // two entities and the redirect
		t.Fatalf("staged items = %d, want 3", n)
	}
	keys, err := r.runs.BufferList(context.Background(), run.ID, "rec/", "", 10)
	if err != nil || len(keys) != 2 {
		t.Fatalf("buffer keys %v %v", keys, err)
	}
	if e1, d1 := r.liveObjects(); e1 != e0 || d1 != d0 {
		t.Fatalf("live content changed: entities %d->%d redirects %d->%d", e0, e1, d0, d1)
	}
	// The plugin may start again now that its run is over.
	if _, err := r.start(""); err != nil {
		t.Fatalf("second run after the first finished: %v", err)
	}
	r.rt.WaitRuns()
}

// A plugin has one active run at a time.
func TestRunSecondStartRefused(t *testing.T) {
	r := newDriverRig(t)
	active, err := r.runs.Create(context.Background(), plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.start(""); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("start while a run is active: %v", err)
	}
	if got, _ := r.runs.Get(context.Background(), active.ID); got.State != plugrun.StateRunning {
		t.Fatalf("the active run was disturbed: %s", got.State)
	}
	if err := r.runs.Fail(context.Background(), active.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.start(""); err != nil {
		t.Fatalf("start after the active run ended: %v", err)
	}
	r.rt.WaitRuns()
}

// A run that has not moved for RecoverAfter is assumed lost and failed by the
// next start; a younger one still blocks.
func TestRunStaleCutoff(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	left := func(age time.Duration) plugrun.Run {
		old := &plugrun.Store{DB: r.db, Now: func() time.Time { return time.Now().Add(-age) }}
		run, err := old.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
		if err != nil {
			t.Fatal(err)
		}
		cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
		if err != nil {
			t.Fatal(err)
		}
		if err := old.SetChangeset(ctx, run.ID, cs); err != nil {
			t.Fatal(err)
		}
		run.ChangesetID = &cs
		return run
	}
	young := left(RecoverAfter - time.Minute)
	if _, err := r.start(""); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("a run younger than RecoverAfter must still block: %v", err)
	}
	if err := r.runs.Fail(ctx, young.ID, "cleanup"); err != nil {
		t.Fatal(err)
	}
	stale := left(RecoverAfter + time.Minute)
	id, err := r.start("")
	if err != nil {
		t.Fatalf("start over a stale run: %v", err)
	}
	r.rt.WaitRuns()
	got, _ := r.runs.Get(ctx, stale.ID)
	if got.State != plugrun.StateFailed || !strings.Contains(got.Reason, "lost") {
		t.Fatalf("stale run: %s %q", got.State, got.Reason)
	}
	if s := r.changesetState(*stale.ChangesetID); s != changeset.StateDiscarded {
		t.Fatalf("stale changeset %s", s)
	}
	if fresh, _ := r.runs.Get(ctx, id); fresh.State != plugrun.StateFinished {
		t.Fatalf("new run %s: %s", fresh.State, fresh.Reason)
	}
}

// At startup a run that has not moved for RecoverAfter was left by a dead
// process: it fails and its changeset is discarded. A run that moved
// recently may belong to another replica and survives.
func TestRunRecoverAtStartup(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	left := func(age time.Duration) (plugrun.Run, int64) {
		old := &plugrun.Store{DB: r.db, Now: func() time.Time { return time.Now().Add(-age) }}
		run, err := old.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
		if err != nil {
			t.Fatal(err)
		}
		cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
		if err != nil {
			t.Fatal(err)
		}
		if err := old.SetChangeset(ctx, run.ID, cs); err != nil {
			t.Fatal(err)
		}
		return run, cs
	}
	live, liveCS := left(time.Minute)
	if err := r.rt.RecoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.runs.Get(ctx, live.ID); got.State != plugrun.StateRunning {
		t.Fatalf("a run another replica may drive was failed: %s", got.State)
	}
	if s := r.changesetState(liveCS); s != changeset.StateStaged {
		t.Fatalf("live changeset %s", s)
	}
	if err := r.runs.Fail(ctx, live.ID, "cleanup"); err != nil {
		t.Fatal(err)
	}
	run, cs := left(RecoverAfter + time.Minute)
	if err := r.rt.RecoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := r.runs.Get(ctx, run.ID)
	if got.State != plugrun.StateFailed || got.Reason == "" {
		t.Fatalf("recovered run: %s %q", got.State, got.Reason)
	}
	if s := r.changesetState(cs); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
	if _, err := r.start(""); err != nil {
		t.Fatalf("start after recovery: %v", err)
	}
	r.rt.WaitRuns()
}

// A step that errors fails the run after one step: no retry, changeset
// discarded.
func TestRunStepErrorFailsRun(t *testing.T) {
	r := newDriverRig(t)
	run := r.finished("error")
	if run.State != plugrun.StateFailed || !strings.Contains(run.Reason, "step failed") || run.Steps != 1 {
		t.Fatalf("run %s steps=%d reason=%q", run.State, run.Steps, run.Reason)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
}

func TestRunFailSignalDiscards(t *testing.T) {
	r := newDriverRig(t)
	run := r.finished("fail")
	if run.State != plugrun.StateFailed || run.Reason != "the source is unreachable" || run.Steps != 1 {
		t.Fatalf("run %s steps=%d reason=%q", run.State, run.Steps, run.Reason)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateDiscarded || r.items(*run.ChangesetID) != 0 {
		t.Fatalf("changeset %s", s)
	}
}

// Once the breaker has opened for on_run_step the driver's first step gets
// ErrCircuitOpen; that fails the run, it is not retried in a loop.
func TestRunCircuitOpenFailsRun(t *testing.T) {
	r := newDriverRig(t)
	for i := 0; i < 3; i++ { // outside a run the plugin's step errors
		if _, err := r.rt.Host().Call(context.Background(), runnerPlugin, sdk.RunStepExport, []byte(`{}`)); err == nil {
			t.Fatal("step outside a run succeeded")
		}
	}
	run := r.finished("")
	if run.State != plugrun.StateFailed || !strings.Contains(run.Reason, "repeated failures") || run.Steps != 1 {
		t.Fatalf("run %s steps=%d reason=%q", run.State, run.Steps, run.Reason)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
}

// A database error the driver cannot get past (here: the move to the
// transform phase) fails the run and discards its changeset. The driver does
// not just return and leave a running row behind.
func TestRunDriverDatabaseErrorFailsRun(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	if _, err := r.db.Pool().Exec(ctx, `
		CREATE OR REPLACE FUNCTION hatamoto_test_block_advance() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'blocked by test'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER hatamoto_test_block_advance BEFORE UPDATE ON hatamoto_runs
		FOR EACH ROW WHEN (NEW.plugin = 'runner' AND NEW.phase = 'transform' AND OLD.phase = 'fetch')
		EXECUTE FUNCTION hatamoto_test_block_advance()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.db.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS hatamoto_test_block_advance ON hatamoto_runs; DROP FUNCTION IF EXISTS hatamoto_test_block_advance()`)
	})
	oldTries, oldWait := driverTries, driverRetryWait
	driverTries, driverRetryWait = 2, time.Millisecond
	t.Cleanup(func() { driverTries, driverRetryWait = oldTries, oldWait })

	run := r.finished("")
	if run.State != plugrun.StateFailed || !strings.Contains(run.Reason, "advance the phase") {
		t.Fatalf("run %s %q", run.State, run.Reason)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
}

// A run older than MaxRunAge is failed before its next step, so it cannot
// outlive its changeset's expiry and finish against it.
func TestRunOlderThanMaxAgeFails(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	old := &plugrun.Store{DB: r.db, Now: func() time.Time { return time.Now().Add(-MaxRunAge - time.Hour) }}
	run, err := old.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.runs.SetChangeset(ctx, run.ID, cs); err != nil {
		t.Fatal(err)
	}
	r.rt.drive(run.ID)
	got, _ := r.runs.Get(ctx, run.ID)
	if got.State != plugrun.StateFailed || !strings.Contains(got.Reason, "took longer") || got.Steps != 0 {
		t.Fatalf("run %s steps=%d %q", got.State, got.Steps, got.Reason)
	}
	if s := r.changesetState(cs); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
}

// Closing the runtime fails the runs its drivers hold and discards their
// changesets, instead of leaving them running for recovery.
func TestRunShutdownFailsRun(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	run, err := r.runs.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.runs.SetChangeset(ctx, run.ID, cs); err != nil {
		t.Fatal(err)
	}
	r.rt.runWG.Add(1)
	go func() { defer r.rt.runWG.Done(); r.rt.drive(run.ID) }()
	r.rt.stopRuns()
	r.rt.WaitRuns()
	got, _ := r.runs.Get(ctx, run.ID)
	if got.State != plugrun.StateFailed || got.Reason != reasonShutdown {
		t.Fatalf("run %s %q", got.State, got.Reason)
	}
	if s := r.changesetState(cs); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
	if _, err := r.start(""); !errors.Is(err, ErrRunsUnavailable) {
		t.Fatalf("start after close: %v", err)
	}
}

// Every run step and every ordinary call gets a cold instance: a guest global
// set in one step is not seen by the next step or by a hook call afterwards
// (ADR 0004 inv. 4). The "leak" run marks guest memory and fails any call
// that finds the mark, so this fails if drive stops using a fresh instance.
func TestRunStepsDoNotShareGuestMemory(t *testing.T) {
	r := newDriverRig(t)
	run := r.finished("leak")
	if run.State != plugrun.StateFinished || run.Steps != 3 {
		t.Fatalf("run %s steps=%d reason=%q", run.State, run.Steps, run.Reason)
	}
	_, err := r.rt.Host().Call(context.Background(), runnerPlugin, sdk.RunStepExport, []byte(`{}`))
	if err == nil || strings.Contains(err.Error(), "leaked") {
		t.Fatalf("an ordinary call after the run: %v", err) // want the trap of a call outside a run, not the mark
	}
}

// A run orphaned after startup (a crash restarted within seconds) is failed
// by the periodic pass once it has been quiet for RecoverAfter.
func TestRunRecoveredPeriodically(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	old := &plugrun.Store{DB: r.db, Now: func() time.Time { return time.Now().Add(-RecoverAfter - time.Minute) }}
	run, err := old.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	r.rt.RecoverRunsEvery(10 * time.Millisecond)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ := r.runs.Get(ctx, run.ID)
		if got.State == plugrun.StateFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the periodic pass left the run %s", got.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A database error on the driver's first read of the run is retried like the
// other driver paths; the driver does not return and leave the run running.
// The runtime's store gets a connection that gives up on a lock after 50 ms;
// the test holds a table lock across the first read and then lets go.
func TestRunDriverRetriesFirstRead(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	oldTries, oldWait := driverTries, driverRetryWait
	driverTries, driverRetryWait = 5, 100*time.Millisecond
	t.Cleanup(func() { driverTries, driverRetryWait = oldTries, oldWait })

	dsn := os.Getenv("DATABASE_URL")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	short, err := database.New(ctx, config.DatabaseConfig{URL: dsn + sep + "lock_timeout=50", MaxConnections: 2, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(short.Close)

	run, err := r.runs.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import",
		Params: json.RawMessage(`{"schema":"` + r.schema + `","tag":"` + r.tag + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.runs.SetChangeset(ctx, run.ID, cs); err != nil {
		t.Fatal(err)
	}
	r.rt.Configure(Config{Services: Services{Runs: &plugrun.Store{DB: short}, Changesets: r.eng}})

	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `LOCK TABLE hatamoto_runs IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); r.rt.drive(run.ID) }()
	time.Sleep(150 * time.Millisecond) // the first read has failed by now
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	got, _ := r.runs.Get(ctx, run.ID)
	if got.State != plugrun.StateFinished {
		t.Fatalf("run %s: %q", got.State, got.Reason)
	}
}

// Starting a run once the runtime is closing returns an error and starts no
// driver, so it cannot Add to the WaitGroup while Close waits on it.
func TestRunStartAfterCloseRefused(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.rt.Close(ctx)
	if _, err := r.start(""); !errors.Is(err, ErrRunsUnavailable) {
		t.Fatalf("start after close: %v", err)
	}
	// A start that passed the early check before Close ran gets as far as
	// spawning its driver.
	run, err := r.runs.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	if r.rt.spawnDriver(run.ID) {
		t.Fatal("a driver was started on a closed runtime")
	}
	r.rt.WaitRuns()
	if got, _ := r.runs.Get(ctx, run.ID); got.State != plugrun.StateRunning {
		t.Fatalf("run %s: nothing should have driven it", got.State)
	}
}
