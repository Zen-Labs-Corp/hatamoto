package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/supervisor"
)

func seedBackup(t *testing.T, data string) string {
	t.Helper()
	dir := filepath.Join(data, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := backup.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	id := backup.NewID(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), backup.KindManual)
	if err := st.Write(backup.Info{ID: id, Kind: backup.KindManual, State: backup.StateOK, Created: time.Now(), Size: 2048}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBackupListDeleteWithoutDatabase(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	id := seedBackup(t, data)
	ctx := context.Background()

	var out bytes.Buffer
	if err := runOps(ctx, []string{"backup", "list"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "2.0 KiB") {
		t.Fatalf("list output: %q", out.String())
	}
	out.Reset()
	if err := runOps(ctx, []string{"backup", "list", "--json"}, nil, &out); err != nil || !strings.Contains(out.String(), `"id": "`+id+`"`) {
		t.Fatalf("json: %v %q", err, out.String())
	}
	out.Reset()
	if err := runOps(ctx, []string{"backup", "delete", id}, nil, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runOps(ctx, []string{"backup", "list"}, nil, &out); err != nil || !strings.Contains(out.String(), "no backups") {
		t.Fatalf("after delete: %v %q", err, out.String())
	}
}

func TestBackupDeleteBadID(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	for _, id := range []string{"../etc/passwd", "nope", ""} {
		err := runBackup(context.Background(), []string{"delete", id}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "not a backup id") {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if err := runBackup(context.Background(), []string{"delete"}, &bytes.Buffer{}); err == nil {
		t.Error("missing id accepted")
	}
}

func TestBackupCreateNeedsDatabase(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	err := runBackup(context.Background(), []string{"create"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "needs the database") {
		t.Fatalf("got %v", err)
	}
}

func TestBackupMutationsRefuseForeignOwner(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	id := seedBackup(t, data)
	old := checkOwner
	t.Cleanup(func() { checkOwner = old })
	checkOwner = func(supervisor.Layout) error { return errors.New("wrong owner") }
	for _, args := range [][]string{{"delete", id}, {"create"}} {
		if err := runBackup(context.Background(), args, &bytes.Buffer{}); err == nil || err.Error() != "wrong owner" {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "backups", id+".json")); err != nil {
		t.Error("backup removed despite owner check")
	}
}

func TestBackupListRemote(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	id := "20260102T030405Z-scheduled"
	srv := offsiteBucket(t, id, nil)
	srv.Put("site/notes.txt", []byte("x"), nil)
	var out bytes.Buffer
	if err := runBackup(context.Background(), []string{"list", "--remote"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || strings.Contains(out.String(), "notes") {
		t.Fatalf("list --remote: %q", out.String())
	}
	out.Reset()
	if err := runBackup(context.Background(), []string{"list", "--remote", "--json"}, &out); err != nil || !strings.Contains(out.String(), `"id": "`+id+`"`) {
		t.Fatalf("json: %v %q", err, out.String())
	}
	if left, _ := os.ReadDir(data); len(left) != 0 {
		t.Fatalf("list --remote wrote %v", left)
	}
}

// The CLI engine has no retention override, so `backup create` prunes by what
// is on disk (the engine reads it after every backup; see the backup package's
// TestRetentionReadEachBackup) and works without the database.
func TestBackupCLIEngineResolvesRetentionFile(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	for _, e := range []string{backup.EnvKeep, backup.EnvKeepPreUpdate, backup.EnvKeepScheduled} {
		t.Setenv(e, "")
	}
	st, err := backup.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		at := time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC)
		if err := st.Write(backup.Info{ID: backup.NewID(at, backup.KindManual), Kind: backup.KindManual, State: backup.StateOK, Created: at, Size: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.SaveRetention(filepath.Join(data, "backups"), map[backup.Kind]int{backup.KindManual: 2}); err != nil {
		t.Fatal(err)
	}
	eng := newBackupEngine(nil, "", "", nil)
	if got := eng.Retention().Manual; got != 2 {
		t.Fatalf("Retention().Manual = %d, want 2 from retention.json", got)
	}
	deleted, err := eng.Prune(eng.Retention().Retention)
	if err != nil || deleted[backup.KindManual] != 2 {
		t.Fatalf("Prune = %v, %v", deleted, err)
	}
	if rows, _ := eng.List(); len(rows) != 2 {
		t.Fatalf("%d backups kept, want 2", len(rows))
	}
}

func TestPrintBackupCreatedMissingMedia(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", "/data")
	info := backup.Info{ID: "b1", Size: 2048, Parts: []string{"secrets.env"}}

	var out bytes.Buffer
	info.MediaMissing = 2
	printBackupCreated(&out, info)
	if !strings.Contains(out.String(), "media files missing: 2\n") {
		t.Errorf("output lacks the missing count:\n%s", out.String())
	}
	for _, want := range []string{"backup b1\n", "size 2.0 KiB\n", "path /data/backups/b1.tar\n", "includes secrets.env\n", "Note: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	info.MediaMissing = 0
	printBackupCreated(&out, info)
	if strings.Contains(out.String(), "missing") {
		t.Errorf("a backup with nothing missing mentions it:\n%s", out.String())
	}
}

func TestPrintBackupCreatedRowlessMedia(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", "/data")
	info := backup.Info{ID: "b1", Size: 2048, MediaRowless: 1234, MediaRowlessSample: []string{"a/one.png", "two.png"}}

	var out bytes.Buffer
	printBackupCreated(&out, info)
	for _, want := range []string{
		"warning: 1,234 files in the media store have no media row and are NOT in this backup:\n",
		"  a/one.png\n  two.png\n", "(showing 2 of 1234)\n", "hatamoto media adopt <dir> [--apply]\n", "backup b1\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	info.MediaRowless, info.MediaRowlessSample = 0, nil
	printBackupCreated(&out, info)
	if strings.Contains(out.String(), "no media row") {
		t.Errorf("a backup with no rowless files warns:\n%s", out.String())
	}
}

func TestPrintBackupCreatedEscapesHostileKeys(t *testing.T) {
	info := backup.Info{ID: "b1", MediaRowless: 2, MediaRowlessSample: []string{"x.png\n  hatamoto media adopt / --apply", "\x1b[2J.png"}}
	var out bytes.Buffer
	printBackupCreated(&out, info)
	if strings.ContainsAny(out.String(), "\x1b") || strings.Contains(out.String(), "\n  hatamoto media adopt / --apply") {
		t.Errorf("output passes control characters through:\n%q", out.String())
	}
}
