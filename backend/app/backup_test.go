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
