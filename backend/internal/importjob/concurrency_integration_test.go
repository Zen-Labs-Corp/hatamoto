//go:build integration

package importjob

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/wpimport"
	"hatamoto.dev/core/migrations"
)

// The job manager against a real Postgres (DATABASE_URL), in a scratch
// database it migrates itself, with a fake engine in place of the importer:
//
//	DATABASE_URL=... go test -race -tags=integration ./internal/importjob
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_importjob_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

type rig struct {
	t       *testing.T
	pool    *pgxpool.Pool
	m       *Manager
	engines atomic.Int32
	release chan struct{} // closed to let a running fake engine finish
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, pool: scratchPool(t), release: make(chan struct{})}
	r.m = New(Config{Pool: r.pool, Key: DeriveKey("jwt-secret-of-the-server-0123456789"), SpoolDir: filepath.Join(t.TempDir(), "spool"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), NoSweepLoop: true})
	r.m.runFn = func(ctx context.Context, id uuid.UUID, _ *record, _ Options, _ Actor, _ string, j *job) {
		r.engines.Add(1)
		select {
		case <-r.release:
		case <-ctx.Done():
			r.m.settle(id, j, ctx.Err())
		}
	}
	t.Cleanup(func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		r.m.Close()
	})
	return r
}

// run inserts a run with a scan report in the given status.
func (r *rig) run(status string) uuid.UUID {
	r.t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), `INSERT INTO hatamoto_import_runs (source, source_ref, status, options, report)
		VALUES ('wordpress', 'wordpress:old.example.test', $1, '{"kind":"rest","url":"https://old.example.test"}', '{"source":"wordpress:old.example.test"}') RETURNING id`, status).Scan(&id)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) status(id uuid.UUID) string {
	r.t.Helper()
	var s string
	if err := r.pool.QueryRow(context.Background(), `SELECT status FROM hatamoto_import_runs WHERE id = $1`, id).Scan(&s); err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *rig) waitFree(id uuid.UUID) {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		if r.m.job(id) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the job did not finish")
}

// fire calls do from n goroutines released together, and returns the errors.
func fire(n int, do func() error) []error {
	errs := make([]error, n)
	var ready, wg sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			errs[i] = do()
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return errs
}

func TestConcurrentStartRunsOneEngine(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		do           func(m *Manager, id uuid.UUID) error
	}{
		{"start", StatusScanned, func(m *Manager, id uuid.UUID) error {
			return m.Start(context.Background(), id, Actor{}, "h", DefaultOptions())
		}},
		{"resume", StatusFailed, func(m *Manager, id uuid.UUID) error { return m.Resume(context.Background(), id, Actor{}, "h", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			id := r.run(tc.status)
			errs := fire(16, func() error { return tc.do(r.m, id) })
			ok := 0
			for _, err := range errs {
				switch {
				case err == nil:
					ok++
				case errors.Is(err, ErrBusy):
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
			if ok != 1 {
				t.Fatalf("%d of 16 concurrent requests were accepted, want 1", ok)
			}
			time.Sleep(100 * time.Millisecond) // a second engine would have started by now
			if n := r.engines.Load(); n != 1 {
				t.Fatalf("%d engines ran, want 1", n)
			}
			if got := r.status(id); got != StatusRunning {
				t.Errorf("status = %s", got)
			}
			// Still one job, and the owner can stop it.
			if err := r.m.Cancel(context.Background(), id, Actor{}); err != nil {
				t.Fatal(err)
			}
			r.waitFree(id)
			if got := r.status(id); got != StatusCanceled {
				t.Errorf("after cancel: %s", got)
			}
		})
	}
}

func TestCancelInTheStartGapWins(t *testing.T) {
	r := newRig(t)
	id := r.run(StatusScanned)
	r.m.gap = func() {
		// The status is already "running" and the job has not launched yet.
		if err := r.m.Cancel(context.Background(), id, Actor{}); err != nil {
			t.Errorf("cancel in the gap: %v", err)
		}
	}
	if err := r.m.Start(context.Background(), id, Actor{}, "h", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	r.waitFree(id)
	if n := r.engines.Load(); n != 0 {
		t.Fatalf("%d engines ran after a cancel", n)
	}
	if got := r.status(id); got != StatusCanceled {
		t.Fatalf("status = %s, want canceled", got)
	}
}

func TestStartIsConditionalOnTheStatus(t *testing.T) {
	r := newRig(t)
	for _, status := range []string{StatusCompleted} {
		id := r.run(status)
		if err := r.m.Start(context.Background(), id, Actor{}, "h", DefaultOptions()); !errors.Is(err, ErrState) {
			t.Errorf("start from %s: %v", status, err)
		}
		if got := r.status(id); got != status {
			t.Errorf("a refused start changed %s to %s", status, got)
		}
	}
}

func TestImportLockIsSharedWithTheCLI(t *testing.T) {
	r := newRig(t)
	id := r.run(StatusScanned)
	ctx := context.Background()

	// The command line holds the lock: the wizard's start is refused, nothing changes.
	release, err := wpimport.AcquireImportLock(ctx, r.pool)
	if err != nil {
		t.Fatal(err)
	}
	err = r.m.Start(ctx, id, Actor{}, "h", DefaultOptions())
	if !errors.Is(err, ErrImportRunning) || !strings.Contains(err.Error(), "another import is running") {
		t.Fatalf("start while the lock is held: %v", err)
	}
	if got := r.status(id); got != StatusScanned || r.engines.Load() != 0 {
		t.Fatalf("a refused start changed the run: %s, %d engines", got, r.engines.Load())
	}
	if r.m.job(id) != nil {
		t.Fatal("a refused start left its job slot")
	}
	release()

	// The wizard's job holds it: the command line is refused.
	if err := r.m.Start(ctx, id, Actor{}, "h", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if _, err := wpimport.AcquireImportLock(ctx, r.pool); !errors.Is(err, wpimport.ErrImportRunning) {
		t.Fatalf("the CLI took the lock during a web import: %v", err)
	}
	if err := r.m.Cancel(ctx, id, Actor{}); err != nil {
		t.Fatal(err)
	}
	r.waitFree(id)
	// Released when the job ends.
	release, err = wpimport.AcquireImportLock(ctx, r.pool)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	release()
}

func TestShutdownMidRunLeavesTheRunInterrupted(t *testing.T) {
	r := newRig(t)
	id := r.run(StatusScanned)
	if err := r.m.Start(context.Background(), id, Actor{}, "h", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	for r.engines.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	r.m.Close()
	run, err := r.m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusInterrupted || run.Error != "" {
		t.Fatalf("after a shutdown: %s %q", run.Status, run.Error)
	}
}

func TestSweepDropsAbandonedCredentials(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	old, fresh := r.run(StatusFailed), r.run(StatusFailed)
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_import_runs SET started_at = now() - interval '9 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{old, fresh} {
		if err := r.m.putSecret(ctx, id, "abcd efgh"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(r.m.cfg.SpoolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := uuid.New()
	for _, id := range []uuid.UUID{old, fresh, orphan} {
		if err := os.WriteFile(r.m.spoolPath(id), []byte("<rss/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, files, err := r.m.Sweep(ctx); err != nil || files != 2 {
		t.Fatalf("sweep: %d files, %v", files, err)
	}
	for id, want := range map[uuid.UUID]bool{old: false, fresh: true} {
		_, has, _ := r.m.getSecret(ctx, id)
		if has != want {
			t.Errorf("secret of %s kept = %v, want %v", id, has, want)
		}
		_, err := os.Stat(r.m.spoolPath(id))
		if (err == nil) != want {
			t.Errorf("spool file of %s kept = %v, want %v", id, err == nil, want)
		}
	}
	if _, err := os.Stat(r.m.spoolPath(orphan)); err == nil {
		t.Error("a spool file without a run was kept")
	}
}
