package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"hatamoto.dev/core/internal/backupcrypt"
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
	a, err = parseRestoreArgs([]string{"b.tar", "--force", "--admin-url", "postgres://su@h/postgres"})
	if err != nil || !a.force || a.dryRun {
		t.Errorf("--force: got %+v, %v", a, err)
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

func TestParseRestoreArgsIdentity(t *testing.T) {
	base := []string{"b.tar.age", "--admin-url", "postgres://su@h/postgres"}
	a, err := parseRestoreArgs(append([]string{"--identity", "k.txt"}, base...))
	if err != nil || a.identity != "k.txt" || a.passphraseStdin || a.source != "b.tar.age" {
		t.Errorf("--identity: %+v, %v", a, err)
	}
	a, err = parseRestoreArgs(append([]string{"--passphrase-stdin"}, base...))
	if err != nil || !a.passphraseStdin || a.identity != "" {
		t.Errorf("--passphrase-stdin: %+v, %v", a, err)
	}
	_, err = parseRestoreArgs(append([]string{"--identity", "k.txt", "--passphrase-stdin"}, base...))
	if err == nil || !strings.Contains(err.Error(), "use either --identity or --passphrase-stdin") {
		t.Errorf("both: err = %v", err)
	}
}

// stdinWith returns a pipe reader holding s; it is never a terminal.
func stdinWith(t *testing.T, s string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	w.Close()
	t.Cleanup(func() { r.Close() })
	return r
}

func TestRestoreIdentityKeyFile(t *testing.T) {
	ident, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	priv := filepath.Join(dir, "key.txt")
	pub := filepath.Join(dir, "pub.txt")
	if err := os.WriteFile(priv, []byte("# created\n"+ident.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte(ident.Recipient().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fn, err := restoreIdentity(restoreArgs{identity: priv}, nil, io.Discard); err != nil {
		t.Errorf("private key: %v", err)
	} else if id, err := fn(); err != nil || id == nil {
		t.Errorf("identity = %v, %v", id, err)
	}
	_, err := restoreIdentity(restoreArgs{identity: pub}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "public key") || strings.Contains(err.Error(), ident.Recipient().String()) {
		t.Errorf("public-only file: err = %v", err)
	}
	if _, err := restoreIdentity(restoreArgs{identity: filepath.Join(dir, "missing")}, nil, io.Discard); err == nil {
		t.Error("missing file: no error")
	}
}

// A bad key file fails before any restore call, so before a download.
func TestBackupRestoreBadIdentityFailsFirst(t *testing.T) {
	restoreEnv(t)
	ident, _ := age.GenerateX25519Identity()
	pub := filepath.Join(t.TempDir(), "pub.txt")
	if err := os.WriteFile(pub, []byte(ident.Recipient().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runBackup(context.Background(), []string{"restore", "--from-offsite", "20260102T030405Z-manual", "--identity", pub, "--admin-url", "postgres://su@h/postgres"}, &out)
	if err == nil || !strings.Contains(err.Error(), "public key") {
		t.Errorf("err = %v, want the public-key error (before any offsite access)", err)
	}
}

func TestRestoreIdentityPassphraseStdinTrimsNewline(t *testing.T) {
	const pass = "correct horse battery staple"
	for _, in := range []string{pass + "\n", pass + "\r\n", pass} {
		fn, err := restoreIdentity(restoreArgs{passphraseStdin: true}, stdinWith(t, in), io.Discard)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		id, err := fn()
		if err != nil {
			t.Fatal(err)
		}
		// The identity opens a backup made with exactly the trimmed passphrase.
		k, err := backupcrypt.PassphraseKey(pass)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		plain := filepath.Join(dir, "a.tar")
		os.WriteFile(plain, []byte("x"), 0o600)
		if err := k.EncryptFile(plain, plain+".age"); err != nil {
			t.Fatal(err)
		}
		if err := id.DecryptFile(plain+".age", filepath.Join(dir, "out.tar")); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestRestoreIdentityNoTerminalNoFlag(t *testing.T) {
	var stderr bytes.Buffer
	fn, err := restoreIdentity(restoreArgs{}, stdinWith(t, ""), &stderr)
	if err != nil {
		t.Fatalf("a plain backup needs no key: %v", err)
	}
	_, err = fn()
	if err == nil || !strings.Contains(err.Error(), "pass --identity") || !strings.Contains(err.Error(), "--passphrase-stdin") {
		t.Errorf("err = %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("prompted without a terminal: %q", stderr.String())
	}
}

func TestRestoreIdentitySecretsNotInErrors(t *testing.T) {
	const pass = "short"
	_, err := restoreIdentity(restoreArgs{passphraseStdin: true}, stdinWith(t, pass+"\n"), io.Discard)
	if err != nil && strings.Contains(err.Error(), pass) {
		t.Errorf("error repeats the passphrase: %v", err)
	}
	if errors.Is(err, backupcrypt.ErrWrongKey) {
		t.Errorf("unexpected err %v", err)
	}
}
