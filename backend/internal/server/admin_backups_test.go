package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backup"
)

// newTestBackups builds the adapter over an engine with no database, so a run
// ends in a recorded failure without touching pg_dump.
func newTestBackups(t *testing.T, ctx context.Context) (*adminBackups, string) {
	t.Helper()
	data := t.TempDir()
	eng := backup.New(backup.Options{Data: data, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), KeepManual: 5, KeepPreUpdate: 2})
	return newAdminBackups(eng, nil, func() context.Context { return ctx }), data
}

func TestAdminBackupsStartRunsInBackgroundAndJoins(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, _ := newTestBackups(t, base)

	reqCtx, reqCancel := context.WithCancel(context.Background())
	id, err := b.Start(reqCtx, "owner@example.com")
	reqCancel() // the request is over; the run must not depend on it
	if err != nil || id == "" {
		t.Fatalf("Start = %q, %v", id, err)
	}
	cancel()
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	b.Wait(wctx)
	if wctx.Err() != nil {
		t.Fatal("Wait did not return: a goroutine is still running")
	}
	st, err := b.List(context.Background())
	if err != nil || len(st.Rows) != 1 || st.Rows[0].ID != id || st.Running {
		t.Fatalf("List = %+v, %v", st, err)
	}
	if st.Rows[0].State != admin.BackupFailed || st.Rows[0].Error == "" {
		t.Fatalf("row = %+v, want a failed row with its error (no database)", st.Rows[0])
	}
}

func TestAdminBackupsSecondStartIsBusy(t *testing.T) {
	b, data := newTestBackups(t, context.Background())
	dir := filepath.Join(data, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := backup.TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := b.Start(context.Background(), ""); !errors.Is(err, admin.ErrBackupRunning) {
		t.Fatalf("Start while locked = %v, want ErrBackupRunning", err)
	}
}

func TestAdminBackupsNotFoundAndList(t *testing.T) {
	b, _ := newTestBackups(t, context.Background())
	if _, _, err := b.Open(context.Background(), "20260101T000000Z-manual"); !errors.Is(err, admin.ErrBackupNotFound) {
		t.Fatalf("Open unknown = %v", err)
	}
	if err := b.Delete(context.Background(), "20260101T000000Z-manual"); !errors.Is(err, admin.ErrBackupNotFound) {
		t.Fatalf("Delete unknown = %v", err)
	}
	if st, err := b.List(context.Background()); err != nil || len(st.Rows) != 0 {
		t.Fatalf("List with the database down = %+v, %v", st, err)
	}
}
