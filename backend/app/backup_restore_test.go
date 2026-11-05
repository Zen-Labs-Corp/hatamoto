package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/s3/s3test"
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

// offsiteBucket serves one backup archive from the fake bucket and points the
// off-site environment at it.
func offsiteBucket(t *testing.T, id string, meta map[string]string) *s3test.Server {
	t.Helper()
	var buf bytes.Buffer
	body := []byte(`{"format":"hatamoto-backup","version":1}`)
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	if meta == nil {
		meta = map[string]string{"sha256": hex.EncodeToString(sum[:])}
	}
	srv := s3test.NewServer(t)
	srv.Put("site/"+id+".tar", buf.Bytes(), meta)
	t.Setenv(offsite.EnvEndpoint, srv.URL)
	t.Setenv(offsite.EnvRegion, s3test.Region)
	t.Setenv(offsite.EnvBucket, s3test.Bucket)
	t.Setenv(offsite.EnvPrefix, "site/")
	t.Setenv(offsite.EnvAccessKey, s3test.AccessKey)
	t.Setenv(offsite.EnvSecretKey, s3test.SecretKey)
	return srv
}

func TestParseRestoreArgsFromOffsite(t *testing.T) {
	a, err := parseRestoreArgs([]string{"--from-offsite", "20260102T030405Z-manual", "--admin-url", "postgres://su@h/postgres", "--offsite-bucket", "b"})
	if err != nil || a.fromOffsite != "20260102T030405Z-manual" || a.source != "" || a.offsite.bucket != "b" {
		t.Fatalf("got %+v, %v", a, err)
	}
	for name, args := range map[string][]string{
		"both":           {"--from-offsite", "x", "y.tar", "--admin-url", "u"},
		"secret on cli":  {"--from-offsite", "x", "--admin-url", "u", "--offsite-secret-key", "k"},
		"offsite no dsn": {"--from-offsite", "x"},
	} {
		if _, err := parseRestoreArgs(args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestBackupRestoreFromOffsiteDryRunLeavesNothing(t *testing.T) {
	data := restoreEnv(t)
	id := "20260102T030405Z-manual"
	srv := offsiteBucket(t, id, nil)
	t.Setenv("MEDIA_UPLOAD_DIR", filepath.Join(t.TempDir(), "uploads"))
	t.Setenv("HATAMOTO_SECRETS_DIR", filepath.Join(t.TempDir(), "secrets"))
	t.Setenv("DATABASE_URL", "postgres://hm:x@127.0.0.1:1/site?sslmode=disable&connect_timeout=1")
	var out bytes.Buffer
	err := runBackup(context.Background(), []string{"restore", "--from-offsite", id, "--dry-run", "--admin-url", "postgres://su@h/postgres"}, &out)
	// The archive was downloaded, its hash proved and the restore checks began;
	// they stop at the unreachable database, as they do for a local archive.
	if err == nil || !strings.Contains(err.Error(), "cannot reach the target database") {
		t.Fatalf("err = %v", err)
	}
	got := 0
	for _, r := range srv.Requests() {
		if r.Method == "GET" && strings.HasSuffix(r.Path, id+".tar") {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("%d downloads", got)
	}
	if left, _ := os.ReadDir(data); len(left) != 0 {
		t.Fatalf("files left in the data dir: %v", left)
	}
}

func TestBackupRestoreFromOffsiteHashMismatch(t *testing.T) {
	data := restoreEnv(t)
	id := "20260102T030405Z-manual"
	offsiteBucket(t, id, map[string]string{"sha256": strings.Repeat("0", 64)})
	var out bytes.Buffer
	err := runBackup(context.Background(), []string{"restore", "--from-offsite", id, "--admin-url", "postgres://su@h/postgres"}, &out)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := os.ReadDir(data); len(left) != 0 {
		t.Fatalf("files left in the data dir: %v", left)
	}
}

func TestBackupRestoreFromOffsiteNeedsATarget(t *testing.T) {
	restoreEnv(t)
	for _, k := range []string{offsite.EnvEndpoint, offsite.EnvRegion, offsite.EnvBucket, offsite.EnvAccessKey, offsite.EnvSecretKey} {
		t.Setenv(k, "")
	}
	var out bytes.Buffer
	err := runBackup(context.Background(), []string{"restore", "--from-offsite", "x", "--admin-url", "u"}, &out)
	if err == nil || !strings.Contains(err.Error(), offsite.EnvSecretKey) {
		t.Fatalf("err = %v", err)
	}
}
