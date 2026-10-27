package app

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/supervisor"
)

func restoreEnv(t *testing.T) (data string) {
	t.Helper()
	data = t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	old := checkOwner
	checkOwner = func(supervisor.Layout) error { return nil }
	t.Cleanup(func() { checkOwner = old })
	return data
}

func TestBackupRestoreUsageErrors(t *testing.T) {
	restoreEnv(t)
	for name, args := range map[string][]string{
		"no admin url":  {"restore", "20260102T030405Z-manual"},
		"no source":     {"restore", "--admin-url", "postgres://su@h/postgres"},
		"two sources":   {"restore", "a", "b", "--admin-url", "postgres://su@h/postgres"},
		"unknown flag":  {"restore", "x", "--admin-url", "u", "--nope"},
		"no subcommand": {},
	} {
		var out bytes.Buffer
		err := runBackup(context.Background(), args, &out)
		if err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}

func TestParseRestoreArgs(t *testing.T) {
	a, err := parseRestoreArgs([]string{"--dry-run", "b.tar", "--admin-url", "postgres://su@h/postgres", "--config-out", "/tmp/c.env"})
	if err != nil {
		t.Fatal(err)
	}
	want := restoreArgs{source: "b.tar", adminURL: "postgres://su@h/postgres", configOut: "/tmp/c.env", dryRun: true}
	if a != want {
		t.Errorf("got %+v, want %+v", a, want)
	}
}

func TestBackupRestoreUnknownIDNamesIt(t *testing.T) {
	restoreEnv(t)
	var out bytes.Buffer
	err := runBackup(context.Background(), []string{"restore", "20260102T030405Z-manual", "--admin-url", "postgres://su@h/postgres"}, &out)
	if err == nil || !strings.Contains(err.Error(), "20260102T030405Z-manual") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupRestoreUnreachableDatabaseTouchesNothing(t *testing.T) {
	data := restoreEnv(t)
	uploads := filepath.Join(t.TempDir(), "uploads")
	secrets := filepath.Join(t.TempDir(), "secrets")
	t.Setenv("MEDIA_UPLOAD_DIR", uploads)
	t.Setenv("HATAMOTO_SECRETS_DIR", secrets)
	t.Setenv("DATABASE_URL", "postgres://hm:x@127.0.0.1:1/site?sslmode=disable&connect_timeout=1")
	// The smallest archive the reader accepts: a manifest and nothing else.
	arch := filepath.Join(data, "min.tar")
	f, err := os.Create(arch)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"format":"hatamoto-backup","version":1}`)
	tw := tar.NewWriter(f)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runBackup(context.Background(), []string{"restore", arch, "--admin-url", "postgres://su@h/postgres"}, &out)
	if err == nil || !strings.Contains(err.Error(), "cannot reach the target database") {
		t.Fatalf("err = %v", err)
	}
	for _, p := range []string{uploads, secrets, filepath.Join(data, "backups")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was created", p)
		}
	}
}

func TestParseCreateFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
		bad  bool
	}{
		{nil, "", false},
		{[]string{"--config", "/etc/h.env"}, "/etc/h.env", false},
		{[]string{"--config=/etc/h.env"}, "/etc/h.env", false},
		{[]string{"extra"}, "", true},
		{[]string{"--nope"}, "", true},
	} {
		got, err := parseCreateFlags(c.args)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%v: got %q, %v", c.args, got, err)
		}
	}
}
