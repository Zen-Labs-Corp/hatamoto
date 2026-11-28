package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/backupcrypt"
)

// newTestBackups builds the adapter over an engine with no database, so a run
// ends in a recorded failure without touching pg_dump.
func newTestBackups(t *testing.T, ctx context.Context) (*adminBackups, string) {
	t.Helper()
	data := t.TempDir()
	eng := backup.New(backup.Options{Data: data, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return newAdminBackups(eng, nil, nil, func() context.Context { return ctx }), data
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
	if _, err := b.Open(context.Background(), "20260101T000000Z-manual"); !errors.Is(err, admin.ErrBackupNotFound) {
		t.Fatalf("Open unknown = %v", err)
	}
	if err := b.Delete(context.Background(), "20260101T000000Z-manual"); !errors.Is(err, admin.ErrBackupNotFound) {
		t.Fatalf("Delete unknown = %v", err)
	}
	if st, err := b.List(context.Background()); err != nil || len(st.Rows) != 0 {
		t.Fatalf("List with the database down = %+v, %v", st, err)
	}
}

func seedOK(t *testing.T, data string, kind backup.Kind, n int) {
	t.Helper()
	st, err := backup.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		at := base.Add(time.Duration(i) * time.Hour)
		if err := st.Write(backup.Info{ID: backup.NewID(at, kind), Kind: kind, State: backup.StateOK, Created: at, Size: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func clearRetentionEnv(t *testing.T) {
	t.Helper()
	for _, e := range []string{backup.EnvKeep, backup.EnvKeepPreUpdate, backup.EnvKeepScheduled} {
		t.Setenv(e, "")
	}
}

func TestAdminBackupsRetentionRoundTrip(t *testing.T) {
	clearRetentionEnv(t)
	b, data := newTestBackups(t, context.Background())
	var svc admin.RetentionService = b // the real adapter is what the admin finds by type assertion
	ctx := context.Background()
	seedOK(t, data, backup.KindManual, 5)
	seedOK(t, data, backup.KindScheduled, 3)

	if v := svc.Retention(); v.Manual != 10 || v.PreUpdate != 5 || v.Scheduled != 7 || len(v.Locked) != 0 {
		t.Fatalf("defaults = %+v", v)
	}
	deleted, err := svc.SaveRetention(ctx, map[string]int{admin.BackupManual: 2})
	if err != nil {
		t.Fatal(err)
	}
	if deleted[admin.BackupManual] != 3 || deleted[admin.BackupScheduled] != 0 {
		t.Fatalf("deleted = %v", deleted)
	}
	st, _ := b.List(ctx)
	if len(st.Rows) != 5 || st.KeepManual != 2 { // 2 manual + 3 scheduled
		t.Fatalf("after save: %d rows, KeepManual %d", len(st.Rows), st.KeepManual)
	}
	if _, err := os.Stat(filepath.Join(data, "backups", backup.RetentionFile)); err != nil {
		t.Fatal("retention.json not written:", err)
	}

	t.Setenv(backup.EnvKeepScheduled, "4")
	v := svc.Retention()
	if v.Scheduled != 4 || !v.Locked[admin.BackupScheduled] || v.EnvVar[admin.BackupScheduled] != backup.EnvKeepScheduled || v.Locked[admin.BackupManual] {
		t.Fatalf("locked view = %+v", v)
	}
	if _, err := svc.SaveRetention(ctx, map[string]int{admin.BackupScheduled: 1}); err == nil {
		t.Fatal("a locked kind was saved")
	}
	if st, _ := b.List(ctx); len(st.Rows) != 5 {
		t.Fatalf("rejected save pruned: %d rows", len(st.Rows))
	}
	if _, err := svc.SaveRetention(ctx, map[string]int{admin.BackupManual: 0}); err == nil {
		t.Fatal("0 accepted")
	}
}

func TestAdminBackupsKeepingFromResolver(t *testing.T) {
	clearRetentionEnv(t)
	t.Setenv(backup.EnvKeepPreUpdate, "9")
	b, data := newTestBackups(t, context.Background())
	if err := backup.SaveRetention(filepath.Join(data, "backups"), map[backup.Kind]int{backup.KindManual: 6}); err != nil {
		t.Fatal(err)
	}
	st, err := b.List(context.Background())
	if err != nil || st.KeepManual != 6 || st.KeepPreUpdate != 9 || st.KeepScheduled != 7 {
		t.Fatalf("List keeps = %d/%d/%d, %v", st.KeepManual, st.KeepPreUpdate, st.KeepScheduled, err)
	}
}

// putBackup writes an ok backup (sidecar and archive) the engine can open.
func putBackup(t *testing.T, data, id string, archive []byte) {
	t.Helper()
	dir := filepath.Join(data, "backups")
	st, err := backup.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write(backup.Info{ID: id, Kind: backup.KindManual, State: backup.StateOK, Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".tar"), archive, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd")
	}
	return len(ents)
}

func TestAdminBackupsOpenEncryptsWithTheSavedKey(t *testing.T) {
	const id = "20260101T000000Z-manual"
	archive := []byte("PLAINTEXT-MARKER secrets.env payload")
	b, data := newTestBackups(t, context.Background())
	putBackup(t, data, id, archive)

	// No key: a plain download.
	f, err := b.Open(context.Background(), id)
	if err != nil || f.Encrypt != nil {
		t.Fatalf("Open without a key = %+v, %v", f, err)
	}
	f.Body.Close()

	key, err := backupcrypt.ParseRecipient(testRecipient)
	if err != nil {
		t.Fatal(err)
	}
	b.keys = fixedKeys{key: key}
	f, err = b.Open(context.Background(), id)
	if err != nil || f.Encrypt == nil {
		t.Fatalf("Open with a key = %+v, %v", f, err)
	}
	defer f.Body.Close()
	var out bytes.Buffer
	wc, err := f.Encrypt(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(wc, f.Body); err != nil {
		t.Fatal(err)
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), backupcrypt.Magic) {
		t.Fatalf("output does not start with the age header: %q", out.Bytes()[:30])
	}
	if bytes.Contains(out.Bytes(), []byte("secrets.env")) || bytes.Contains(out.Bytes(), []byte("PLAINTEXT-MARKER")) {
		t.Fatal("the encrypted download contains archive plaintext")
	}
}

func TestAdminBackupsOpenRefusesAnUnreadableKey(t *testing.T) {
	const id = "20260101T000000Z-manual"
	b, data := newTestBackups(t, context.Background())
	putBackup(t, data, id, []byte("tar"))
	for _, kerr := range []error{backupcrypt.ErrUnreadable, errors.New("database down")} {
		b.keys = fixedKeys{err: kerr}
		before := openFDs(t)
		f, err := b.Open(context.Background(), id)
		if !errors.Is(err, admin.ErrBackupKeyUnreadable) || f.Body != nil || f.Encrypt != nil {
			t.Fatalf("Open with key error %v = %+v, %v", kerr, f, err)
		}
		if after := openFDs(t); after != before {
			t.Fatalf("file descriptors %d -> %d: the archive was not closed", before, after)
		}
	}
}
