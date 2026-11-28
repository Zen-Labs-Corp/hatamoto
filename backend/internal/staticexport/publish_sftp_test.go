package staticexport

import (
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/runenv"
)

func sftpCfg(srv *sftpTestServer) SFTPConfig {
	return SFTPConfig{Host: srv.Host, Port: srv.Port, User: testSFTPUser, Auth: SFTPAuthPassword,
		Secret: testSFTPPassword, Dir: "public_html", HostKey: srv.Fingerprint, AllowPrivate: true}
}

func newTestSFTPTarget(t *testing.T, srv *sftpTestServer, mod func(*SFTPConfig)) *SFTPTarget {
	t.Helper()
	cfg := sftpCfg(srv)
	if mod != nil {
		mod(&cfg)
	}
	tgt, err := NewSFTPTarget(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tgt
}

func readRoot(t *testing.T, srv *sftpTestServer, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(srv.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeRoot(t *testing.T, srv *sftpTestServer, rel, body string) {
	t.Helper()
	full := filepath.Join(srv.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSFTPTarget_UploadsChangedDeletesRemovedManifestLast(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	writeRoot(t, srv, "public_html/old.html", "old")
	writeRoot(t, srv, "public_html/keep.html", "foreign file the export never wrote")

	staged, next := stageFiles(t, map[string]string{"index.html": "<p>hi</p>", "blog/post/index.html": "<p>post</p>"})
	d := Diff{Changed: []string{"index.html", "blog/post/index.html"}, Removed: []string{"old.html"}}
	if err := tgt.Publish(context.Background(), staged, d, next); err != nil {
		t.Fatal(err)
	}
	if got := readRoot(t, srv, "public_html/index.html"); got != "<p>hi</p>" {
		t.Errorf("index.html = %q", got)
	}
	if got := readRoot(t, srv, "public_html/blog/post/index.html"); got != "<p>post</p>" {
		t.Errorf("post = %q", got)
	}
	if _, err := os.Stat(filepath.Join(srv.Root, "public_html", "old.html")); !os.IsNotExist(err) {
		t.Errorf("old.html still there: %v", err)
	}
	if got := readRoot(t, srv, "public_html/keep.html"); got == "" {
		t.Error("a file the manifest never listed was touched")
	}
	m, err := ReadManifest(strings.NewReader(readRoot(t, srv, "public_html/"+ManifestFile)))
	if err != nil || len(m.Files) != 2 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if left := leftoverTmp(t, srv.Root); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if !tgt.Modified() || !tgt.Deleted() {
		t.Errorf("Modified=%v Deleted=%v", tgt.Modified(), tgt.Deleted())
	}
}

func TestSFTPTarget_PreviousFirstExportAndRoundTrip(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	if _, found, err := tgt.Previous(context.Background()); err != nil || found {
		t.Fatalf("first export: found=%v err=%v", found, err)
	}
	staged, next := stageFiles(t, map[string]string{"index.html": "x"})
	if err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next); err != nil {
		t.Fatal(err)
	}
	got, found, err := tgt.Previous(context.Background())
	if err != nil || !found || got.Files["index.html"].SHA256 != "sum-index.html" {
		t.Fatalf("previous = %+v found=%v err=%v", got, found, err)
	}
}

func TestSFTPTarget_UploadErrorDeletesNothing(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	writeRoot(t, srv, "public_html/old.html", "old")
	writeRoot(t, srv, "public_html/"+ManifestFile, "old manifest")
	staged, next := stageFiles(t, map[string]string{"a.html": "a"})
	d := Diff{Changed: []string{"a.html", "missing.html"}, Removed: []string{"old.html"}}
	if err := tgt.Publish(context.Background(), staged, d, next); err == nil {
		t.Fatal("publish succeeded with a missing staged file")
	}
	if readRoot(t, srv, "public_html/old.html") != "old" || readRoot(t, srv, "public_html/"+ManifestFile) != "old manifest" {
		t.Error("an upload error still deleted or wrote the manifest")
	}
}

func TestSFTPTarget_RefusesUntrustedHostKey(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.HostKey = "" })
	staged, next := stageFiles(t, map[string]string{"a.html": "a"})
	err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"a.html"}}, next)
	if !errors.Is(err, ErrSFTPHostKeyUntrusted) || !strings.Contains(err.Error(), "Trust") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := tgt.Previous(context.Background()); !errors.Is(err, ErrSFTPHostKeyUntrusted) {
		t.Fatalf("Previous err = %v", err)
	}
	if srv.Conns() != 0 {
		t.Errorf("connected %d times without a pinned key", srv.Conns())
	}
}

func TestSFTPTarget_RefusesChangedHostKey(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.HostKey = "SHA256:someOtherKey" })
	staged, next := stageFiles(t, map[string]string{"a.html": "a"})
	err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"a.html"}}, next)
	if !errors.Is(err, ErrSFTPHostKeyChanged) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), srv.Fingerprint) || strings.Contains(err.Error(), testSFTPPassword) {
		t.Errorf("error text = %q", err)
	}
	if _, statErr := os.Stat(filepath.Join(srv.Root, "public_html")); !os.IsNotExist(statErr) {
		t.Errorf("a write happened: %v", statErr)
	}
}

func TestProbeSFTPHostKey(t *testing.T) {
	srv := newSFTPServer(t)
	fp, err := ProbeSFTPHostKey(context.Background(), srv.Host, srv.Port, true, nil)
	if err != nil || fp != srv.Fingerprint {
		t.Fatalf("fp = %q err = %v, want %q", fp, err, srv.Fingerprint)
	}
}

func TestSFTPTarget_MergesRemoteHTAccess(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.HTAccess = true })
	if !tgt.WritesHTAccess() {
		t.Fatal("WritesHTAccess is false")
	}
	writeRoot(t, srv, "public_html/.htaccess", "Options -Indexes\n")
	staged, next := stageFiles(t, map[string]string{"index.html": "x"})
	block := "# BEGIN Hatamoto\n<IfModule mod_alias.c>\nRedirectMatch 301 ^/a/?$ /b\n</IfModule>\n# END Hatamoto\n"
	if err := os.WriteFile(filepath.Join(staged, HTAccessFile), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next); err != nil {
		t.Fatal(err)
	}
	if got := readRoot(t, srv, "public_html/.htaccess"); got != "Options -Indexes\n"+block {
		t.Fatalf(".htaccess = %q", got)
	}
	if strings.Contains(readRoot(t, srv, "public_html/"+ManifestFile), HTAccessFile) {
		t.Error("the manifest lists .htaccess")
	}

	// No more redirects: the block goes, the owner's line stays byte for byte.
	if err := os.WriteFile(filepath.Join(staged, HTAccessFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tgt.Publish(context.Background(), staged, Diff{}, next); err != nil {
		t.Fatal(err)
	}
	if got := readRoot(t, srv, "public_html/.htaccess"); got != "Options -Indexes\n" {
		t.Fatalf(".htaccess after = %q", got)
	}
	if left := leftoverTmp(t, srv.Root); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSFTPTarget_HTAccessOffRemovesTheBlock(t *testing.T) {
	const block = "# BEGIN Hatamoto\n<IfModule mod_alias.c>\nRedirectMatch 301 ^/a/?$ /b\n</IfModule>\n# END Hatamoto\n"
	publish := func(t *testing.T, remote string) (*sftpTestServer, error) {
		srv := newSFTPServer(t)
		tgt := newTestSFTPTarget(t, srv, nil)
		if remote != "" {
			writeRoot(t, srv, "public_html/.htaccess", remote)
		}
		staged, next := stageFiles(t, map[string]string{"index.html": "x"})
		return srv, tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next)
	}

	t.Run("keeps the owner's lines", func(t *testing.T) {
		srv, err := publish(t, "Options -Indexes\n"+block+"AddType x y\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := readRoot(t, srv, "public_html/.htaccess"); got != "Options -Indexes\nAddType x y\n" {
			t.Fatalf(".htaccess = %q", got)
		}
	})
	t.Run("deletes a file that held only the block", func(t *testing.T) {
		srv, err := publish(t, block)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(srv.Root, "public_html", ".htaccess")); !os.IsNotExist(err) {
			t.Fatalf(".htaccess still there: %v", err)
		}
	})
	t.Run("leaves a file without a block", func(t *testing.T) {
		srv, err := publish(t, "Options -Indexes\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := readRoot(t, srv, "public_html/.htaccess"); got != "Options -Indexes\n" {
			t.Fatalf(".htaccess = %q", got)
		}
	})
	t.Run("creates nothing when there is no file", func(t *testing.T) {
		srv, err := publish(t, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(srv.Root, "public_html", ".htaccess")); !os.IsNotExist(err) {
			t.Fatalf(".htaccess created: %v", err)
		}
	})
}

func TestSFTPTarget_HTAccessRemovedWhenFileWasOnlyTheBlock(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.HTAccess = true })
	writeRoot(t, srv, "public_html/.htaccess", "# BEGIN Hatamoto\nold\n# END Hatamoto\n")
	staged, next := stageFiles(t, map[string]string{"index.html": "x"})
	if err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.Root, "public_html", ".htaccess")); !os.IsNotExist(err) {
		t.Fatalf(".htaccess still there: %v", err)
	}
}

func TestSFTPTarget_KeyAuth(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.Auth, c.Secret = SFTPAuthKey, srv.ClientKeyPEM })
	if _, found, err := tgt.Previous(context.Background()); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestSFTPTarget_WrongPassword(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.Secret = "not-the-password" })
	_, _, err := tgt.Previous(context.Background())
	if err == nil || strings.Contains(err.Error(), "not-the-password") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSFTPKey_EncryptedRefused(t *testing.T) {
	_, priv := newTestSigner(t)
	raw, err := ssh.ParseRawPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(raw, "", []byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseSFTPKey(string(pem.EncodeToMemory(blk)))
	if !errors.Is(err, ErrSFTPKeyPassphrase) || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewSFTPTarget(SFTPConfig{Host: "h.example.com", Port: 22, Dir: "x", Auth: SFTPAuthKey, Secret: string(pem.EncodeToMemory(blk))}); !errors.Is(err, ErrSFTPKeyPassphrase) {
		t.Fatalf("NewSFTPTarget err = %v", err)
	}
	if _, err := ParseSFTPKey("not a key"); err == nil {
		t.Fatal("garbage parsed as a key")
	}
}

func TestSFTPTarget_RenameFallback(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	tgt.noPosixRename = true
	writeRoot(t, srv, "public_html/index.html", "old")
	staged, next := stageFiles(t, map[string]string{"index.html": "new"})
	if err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next); err != nil {
		t.Fatal(err)
	}
	if got := readRoot(t, srv, "public_html/index.html"); got != "new" {
		t.Errorf("index.html = %q", got)
	}
	if left := leftoverTmp(t, srv.Root); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSFTPTarget_LoopbackNeedsOptIn(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, func(c *SFTPConfig) { c.AllowPrivate = false })
	_, _, err := tgt.Previous(context.Background())
	if err == nil || !strings.Contains(err.Error(), EnvSFTPAllowPrivate) {
		t.Fatalf("err = %v", err)
	}
	if srv.Conns() != 0 {
		t.Errorf("connected %d times to loopback without the opt-in", srv.Conns())
	}
	if _, err := ProbeSFTPHostKey(context.Background(), srv.Host, srv.Port, false, nil); err == nil || srv.Conns() != 0 {
		t.Errorf("probe reached loopback: err=%v conns=%d", err, srv.Conns())
	}
}

func TestSFTPTarget_KeyAndDescribeHaveNoSecret(t *testing.T) {
	tgt, err := NewSFTPTarget(SFTPConfig{Host: "ftp.example.com", Port: 2222, User: "site", Auth: SFTPAuthPassword,
		Secret: "topsecret", Dir: "public_html/"})
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Key() != "sftp:site@ftp.example.com:2222/public_html" || tgt.Describe() != "sftp://site@ftp.example.com:2222/public_html" {
		t.Errorf("key = %q describe = %q", tgt.Key(), tgt.Describe())
	}
	if strings.Contains(tgt.Key()+tgt.Describe(), "topsecret") {
		t.Error("the secret is in the key or description")
	}
}

func TestNewSFTPTarget_Refusals(t *testing.T) {
	base := SFTPConfig{Host: "h.example.com", Port: 22, User: "u", Auth: SFTPAuthPassword, Secret: "x", Dir: "public_html"}
	for name, mod := range map[string]func(*SFTPConfig){
		"absolute dir": func(c *SFTPConfig) { c.Dir = "/etc" },
		"parent dir":   func(c *SFTPConfig) { c.Dir = "a/../b" },
		"bad port":     func(c *SFTPConfig) { c.Port = 0 },
		"bad auth":     func(c *SFTPConfig) { c.Auth = "token" },
	} {
		c := base
		mod(&c)
		if _, err := NewSFTPTarget(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckSFTPHost(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "10.0.0.5", "192.168.1.1", "::1", "169.254.1.1", "a.localhost"} {
		if err := CheckSFTPHost(h); !errors.Is(err, ErrPrivateSFTPHost) || !strings.Contains(err.Error(), EnvSFTPAllowPrivate) {
			t.Errorf("CheckSFTPHost(%q) = %v", h, err)
		}
	}
	for _, h := range []string{"ftp.example.com", "93.184.216.34"} {
		if err := CheckSFTPHost(h); err != nil {
			t.Errorf("CheckSFTPHost(%q) = %v", h, err)
		}
	}
	if AllowPrivateSFTP(nil) || AllowPrivateSFTP(func(string) string { return "" }) || !AllowPrivateSFTP(func(string) string { return "1" }) {
		t.Error("AllowPrivateSFTP")
	}
}

func TestSFTPTarget_Suppressed(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	staged, next := stageFiles(t, map[string]string{"a.html": "a"})
	if err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"a.html"}}, next); !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("err = %v", err)
	}
}

func TestSFTPTarget_PreviousRefusesForgedManifestKeys(t *testing.T) {
	for _, key := range []string{"../.ssh/authorized_keys", "a/../../x", "/etc/passwd", ".htaccess", "sub/.htaccess", ManifestFile, `a\b`} {
		srv := newSFTPServer(t)
		tgt := newTestSFTPTarget(t, srv, nil)
		writeRoot(t, srv, ".ssh/authorized_keys", "ssh-ed25519 AAAA\n")
		writeRoot(t, srv, "public_html/.htaccess", "Options -Indexes\n")
		forged, err := jsonx.MarshalPinned(map[string]any{"version": manifestVersion, "files": map[string]any{key: map[string]any{}}})
		if err != nil {
			t.Fatal(err)
		}
		writeRoot(t, srv, "public_html/"+ManifestFile, string(forged))
		_, _, err = tgt.Previous(context.Background())
		if err == nil || !strings.Contains(err.Error(), "outside the export") {
			t.Errorf("key %q: err = %v", key, err)
		}
		if readRoot(t, srv, ".ssh/authorized_keys") == "" || readRoot(t, srv, "public_html/.htaccess") == "" {
			t.Errorf("key %q: a file outside the export was touched", key)
		}
	}
}

func TestSFTPTarget_PreviousCapsTheManifest(t *testing.T) {
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	writeRoot(t, srv, "public_html/"+ManifestFile, strings.Repeat(" ", sftpMaxManifest+1))
	if _, _, err := tgt.Previous(context.Background()); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v", err)
	}
}
