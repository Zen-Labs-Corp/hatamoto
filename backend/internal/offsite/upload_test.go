package offsite

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"hatamoto.dev/core/internal/backupcrypt"
	"hatamoto.dev/core/internal/s3/s3test"
)

func writeArchive(t *testing.T, size int) (path string, data []byte) {
	t.Helper()
	data = make([]byte, size)
	_, _ = rand.Read(data)
	path = filepath.Join(t.TempDir(), idN(9)+".tar")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestUploadVerifiesAndPrunes(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "site/")
	for n := 1; n <= 3; n++ {
		srv.Put("site/"+idN(n)+".tar", []byte("old"), nil)
	}
	path, data := writeArchive(t, 4096)
	res, err := Upload(t.Context(), c, "site/", idN(9), path, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Size != 4096 || res.SHA256 != sum(data) || res.At.IsZero() || res.Error != "" {
		t.Fatalf("res = %+v", res)
	}
	got := srv.Objects()
	obj := got["site/"+idN(9)+".tar"]
	if !bytes.Equal(obj.Data, data) || obj.Meta["sha256"] != sum(data) || obj.Meta["hatamoto-id"] != idN(9) {
		t.Fatalf("object = %d bytes, meta %v", len(obj.Data), obj.Meta)
	}
	if len(got) != 2 {
		t.Fatalf("bucket holds %d objects after prune to 2", len(got))
	}
	if _, ok := got["site/"+idN(3)+".tar"]; !ok {
		t.Fatal("the newest old backup was pruned")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("local archive removed after upload")
	}
}

func TestUploadFailsWhenTheObjectIsTruncated(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.Tamper = func(_ string, o *s3test.Object) { o.Data = o.Data[:len(o.Data)-1] }
	c := testClient(t, srv, "")
	srv.Put(idN(1)+".tar", []byte("older good copy"), nil)
	path, _ := writeArchive(t, 1000)
	res, err := Upload(t.Context(), c, "", idN(9), path, 1, nil)
	if !errors.Is(err, ErrVerify) || res.OK || res.Error == "" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	got := srv.Objects()
	if _, ok := got[idN(9)+".tar"]; ok {
		t.Fatal("unverified object left in the bucket")
	}
	if _, ok := got[idN(1)+".tar"]; !ok {
		t.Fatal("older backup deleted before a verified upload")
	}
}

func TestUploadFailsWhenTheRecordedHashDiffers(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.Tamper = func(_ string, o *s3test.Object) { o.Meta["sha256"] = strings.Repeat("0", 64) }
	c := testClient(t, srv, "")
	path, _ := writeArchive(t, 1000)
	res, err := Upload(t.Context(), c, "", idN(9), path, 5, nil)
	if !errors.Is(err, ErrVerify) || res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(srv.Objects()) != 0 {
		t.Fatal("unverified object left in the bucket")
	}
}

func TestUploadFailuresDoNotPrune(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "")
	for n := 1; n <= 3; n++ {
		srv.Put(idN(n)+".tar", []byte("old"), nil)
	}
	if res, err := Upload(t.Context(), c, "", idN(9), filepath.Join(t.TempDir(), "missing.tar"), 1, nil); err == nil || res.OK {
		t.Fatalf("missing file: %+v, %v", res, err)
	}
	if res, err := Upload(t.Context(), c, "", "bad id", "x", 1, nil); err == nil || res.OK {
		t.Fatalf("bad id: %+v, %v", res, err)
	}
	if len(srv.Objects()) != 3 {
		t.Fatal("objects deleted by a failed upload")
	}
}

func TestUploadErrorsAndLogsNeverContainTheSecret(t *testing.T) {
	srv := s3test.NewServer(t)
	tg := testTarget(srv, "")
	tg.SecretKey = "distinct-wrong-secret-9f3a" // gitleaks:allow
	c, err := tg.Client(nil)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	path, _ := writeArchive(t, 100)
	res, err := Upload(context.Background(), c, "", idN(9), path, 1, nil)
	if err == nil || res.OK || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	for _, s := range []string{err.Error(), res.Error, logs.String()} {
		if strings.Contains(s, tg.SecretKey) {
			t.Fatalf("secret key leaked: %q", s)
		}
	}
}

func TestUploadLargeUsesMultipart(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "")
	path, data := writeArchive(t, 17<<20) // over one 16 MiB part
	res, err := Upload(t.Context(), c, "", idN(9), path, 3, nil)
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !bytes.Equal(srv.Objects()[idN(9)+".tar"].Data, data) {
		t.Fatal("multipart object differs from the local file")
	}
	if srv.OpenUploads() != 0 {
		t.Fatal("open multipart upload")
	}
}

func testKey(t *testing.T) (*backupcrypt.Key, *backupcrypt.Identity) {
	t.Helper()
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	k, err := backupcrypt.ParseRecipient(ident.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	id, err := backupcrypt.ParseIdentityFile(strings.NewReader(ident.String() + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return k, id
}

func noTempLeft(t *testing.T, dir string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestUploadEncryptsACopy(t *testing.T) {
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "site/")
	key, ident := testKey(t)
	path := filepath.Join(t.TempDir(), idN(9)+".tar")
	plain := []byte("secrets.env core.dump marker")
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Upload(t.Context(), c, "site/", idN(9), path, 3, key)
	if err != nil || !res.OK || !res.Encrypted {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	got := srv.Objects()
	if _, ok := got["site/"+idN(9)+".tar"]; ok || len(got) != 1 {
		t.Fatalf("objects = %v", got)
	}
	obj := got["site/"+idN(9)+".tar.age"]
	if !strings.HasPrefix(string(obj.Data), backupcrypt.Magic) || bytes.Contains(obj.Data, []byte("secrets.env")) {
		t.Fatal("stored bytes are not ciphertext")
	}
	if res.SHA256 != sum(obj.Data) || res.Size != int64(len(obj.Data)) || obj.Meta["sha256"] != res.SHA256 ||
		obj.Meta["hatamoto-encrypted"] != "age" || obj.Meta["hatamoto-id"] != idN(9) {
		t.Fatalf("res = %+v, meta = %v", res, obj.Meta)
	}
	noTempLeft(t, filepath.Dir(path))
	if b, _ := os.ReadFile(path); !bytes.Equal(b, plain) {
		t.Fatal("local archive changed")
	}
	enc := filepath.Join(t.TempDir(), "stored.tar.age")
	if err := os.WriteFile(enc, obj.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	dec := filepath.Join(t.TempDir(), "out.tar")
	if err := ident.DecryptFile(enc, dec); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dec); !bytes.Equal(b, plain) {
		t.Fatal("decrypted copy differs from the archive")
	}
}

func TestUploadEncryptedCleansUpAfterAFailedPut(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.FailPut(func(string) bool { return true })
	c := testClient(t, srv, "")
	key, _ := testKey(t)
	path, _ := writeArchive(t, 1000)
	res, err := Upload(t.Context(), c, "", idN(9), path, 1, key)
	if err == nil || res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	noTempLeft(t, filepath.Dir(path))
}

func TestUploadEncryptedStillVerifies(t *testing.T) {
	key, _ := testKey(t)
	for name, tamper := range map[string]func(string, *s3test.Object){
		"truncated": func(_ string, o *s3test.Object) { o.Data = o.Data[:len(o.Data)-1] },
		"hash":      func(_ string, o *s3test.Object) { o.Meta["sha256"] = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := s3test.NewServer(t)
			srv.Tamper = tamper
			c := testClient(t, srv, "")
			path, _ := writeArchive(t, 1000)
			res, err := Upload(t.Context(), c, "", idN(9), path, 1, key)
			if !errors.Is(err, ErrVerify) || res.OK {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			if len(srv.Objects()) != 0 {
				t.Fatal("unverified object left in the bucket")
			}
			noTempLeft(t, filepath.Dir(path))
		})
	}
}
