package offsite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/s3"
	"hatamoto.dev/core/internal/s3/s3test"
)

func testTarget(srv *s3test.Server, prefix string) Target {
	return Target{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket, Prefix: prefix,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}
}

func testClient(t *testing.T, srv *s3test.Server, prefix string) *s3.Client {
	t.Helper()
	c, err := testTarget(srv, prefix).Client(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func idN(n int) string { return fmt.Sprintf("202611%02dT030000Z-scheduled", n) }

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestPruneKeepsNewestAndLeavesForeignKeys(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "site/")
	for n := 1; n <= 5; n++ {
		srv.Put("site/"+idN(n)+".tar", []byte("x"), nil)
	}
	foreign := []string{"site/other.txt", "site/nested/" + idN(1) + ".tar", "site/notanid.tar",
		"site/" + idN(1) + ".tar.bak", "elsewhere/" + idN(1) + ".tar", "site/20261101T030000Z-bogus.tar"}
	for _, k := range foreign {
		srv.Put(k, []byte("keep me"), nil)
	}
	deleted, err := Prune(t.Context(), c, "site/", 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deleted, ",") != "site/"+idN(2)+".tar,site/"+idN(1)+".tar" {
		t.Fatalf("deleted = %v", deleted)
	}
	got := srv.Objects()
	for n := 3; n <= 5; n++ {
		if _, ok := got["site/"+idN(n)+".tar"]; !ok {
			t.Errorf("newest %d deleted", n)
		}
	}
	for _, k := range foreign {
		if _, ok := got[k]; !ok {
			t.Errorf("foreign key %s deleted", k)
		}
	}
	if d, err := Prune(t.Context(), c, "site/", 3); err != nil || len(d) != 0 {
		t.Fatalf("second prune = %v, %v", d, err)
	}
	if _, err := Prune(t.Context(), c, "site/", 0); err == nil {
		t.Fatal("keep 0 accepted")
	}
}

func TestListRemoteNewestFirst(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.PageSize = 2
	c := testClient(t, srv, "")
	for _, n := range []int{2, 5, 3} {
		srv.Put(idN(n)+".tar", []byte("abc"), nil)
	}
	srv.Put("junk", []byte("x"), nil)
	got, err := ListRemote(t.Context(), c, "")
	if err != nil || len(got) != 3 || got[0].ID != idN(5) || got[2].ID != idN(2) || got[0].Size != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDownloadVerifiesHash(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "p/")
	data := []byte("the whole backup")
	srv.Put("p/"+idN(1)+".tar", data, map[string]string{"sha256": sum(data)})
	dir := t.TempDir()
	path, err := Download(t.Context(), c, "p/", idN(1), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != ".tmp-"+idN(1)+".tar" {
		t.Fatalf("path = %s", path)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, data) {
		t.Fatal("downloaded bytes differ")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}

	// Bytes that do not match the recorded hash are removed.
	srv.Put("p/"+idN(2)+".tar", []byte("tampered"), map[string]string{"sha256": sum(data)})
	_, err = Download(t.Context(), c, "p/", idN(2), dir)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tmp-"+idN(2)+".tar")); !os.IsNotExist(err) {
		t.Fatal("mismatching download left behind")
	}
	// No recorded hash, missing object, invalid id.
	srv.Put("p/"+idN(3)+".tar", data, nil)
	if _, err := Download(t.Context(), c, "p/", idN(3), dir); err == nil || !strings.Contains(err.Error(), "no recorded checksum") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Download(t.Context(), c, "p/", idN(4), dir); err == nil || !strings.Contains(err.Error(), "not in the bucket") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Download(t.Context(), c, "p/", "../x", dir); err == nil {
		t.Fatal("invalid id accepted")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(left) != 0 {
		t.Fatalf("files left: %v", left)
	}
}

func TestTargetFromEnv(t *testing.T) {
	env := map[string]string{EnvRegion: "eu-west-1", EnvBucket: "my-backups", EnvAccessKey: "AK",
		EnvSecretKey: "sk-value", EnvPrefix: "site/"}
	get := func(k string) string { return env[k] }
	tg, err := TargetFromEnv(get)
	if err != nil || tg.Endpoint != "https://s3.eu-west-1.amazonaws.com" || tg.Prefix != "site/" {
		t.Fatalf("target = %+v, %v", tg, err)
	}
	env[EnvEndpoint] = "https://acct.r2.cloudflarestorage.com"
	if tg, _ = TargetFromEnv(get); tg.Endpoint != "https://acct.r2.cloudflarestorage.com" {
		t.Fatalf("endpoint = %s", tg.Endpoint)
	}
	env[EnvEndpoint] = "http://example.com"
	if _, err = TargetFromEnv(get); err == nil {
		t.Fatal("plain http to a public host accepted")
	}
	delete(env, EnvEndpoint)
	delete(env, EnvSecretKey)
	_, err = TargetFromEnv(get)
	if err == nil || !strings.Contains(err.Error(), EnvSecretKey) {
		t.Fatalf("err = %v", err)
	}
	env[EnvSecretKey] = "sk-value"
	env[EnvBucket] = "X"
	if _, err = TargetFromEnv(get); err == nil || strings.Contains(err.Error(), "sk-value") {
		t.Fatalf("err = %v", err)
	}
}

func TestSettingsTarget(t *testing.T) {
	key := SecretKey("s")
	sealed, _ := SealSecret(key, "sk-value")
	s := valid()
	s.SecretSealed = sealed
	tg, ok := s.Target(key)
	if !ok || tg.SecretKey != "sk-value" || tg.Endpoint != "https://s3.eu-west-1.amazonaws.com" || tg.Bucket != "my-backups" {
		t.Fatalf("target = %+v, %v", tg, ok)
	}
	if _, ok := s.Target(SecretKey("other")); ok {
		t.Fatal("opened with the wrong key")
	}
}
