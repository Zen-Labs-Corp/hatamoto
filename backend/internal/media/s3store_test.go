package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/s3/s3test"
)

func newTestS3Store(t *testing.T, prefix string) (*S3Store, *s3test.Server) {
	t.Helper()
	fake := s3test.NewServer(t)
	st, err := NewS3Store(S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: prefix, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	return st, fake
}

func TestS3StoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	spool := t.TempDir()
	t.Setenv("TMPDIR", spool)
	st, fake := newTestS3Store(t, "site-a/")
	body := "hello object storage"
	n, err := st.Put(ctx, "tenant/abc.txt", strings.NewReader(body))
	if err != nil || n != int64(len(body)) {
		t.Fatalf("Put = %d, %v", n, err)
	}
	obj, ok := fake.Objects()["site-a/tenant/abc.txt"]
	if !ok {
		t.Fatalf("prefix must be verbatim, objects = %v", fake.Objects())
	}
	sum := sha256.Sum256([]byte(body))
	if obj.Meta["sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 meta = %q", obj.Meta["sha256"])
	}

	rc, err := st.Get(ctx, "tenant/abc.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != body {
		t.Fatalf("Get = %q", got)
	}

	info, err := st.Stat(ctx, "tenant/abc.txt")
	if err != nil || info.Size != int64(len(body)) || info.SHA256 != hex.EncodeToString(sum[:]) || info.ETag == "" {
		t.Fatalf("Stat = %+v, %v", info, err)
	}

	list, err := st.List(ctx, "tenant/")
	if err != nil || len(list) != 1 || list[0].Key != "tenant/abc.txt" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	oi, f, err := st.Open(ctx, "tenant/abc.txt")
	if err != nil || oi.Size != int64(len(body)) {
		t.Fatalf("Open = %+v, %v", oi, err)
	}
	if _, err := f.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	tail, _ := io.ReadAll(f)
	if string(tail) != body[6:] {
		t.Errorf("seeked read = %q", tail)
	}
	f.Close()
	if left, _ := os.ReadDir(spool); len(left) != 0 {
		t.Errorf("Open left a temp file behind")
	}

	if err := st.Delete(ctx, "tenant/abc.txt"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) != 0 {
		t.Errorf("object not deleted")
	}
	if st.URL("x/y") != "/media/x/y" {
		t.Errorf("URL = %q", st.URL("x/y"))
	}
	if err := st.Test(ctx); err != nil {
		t.Errorf("Test = %v", err)
	}
}

func TestS3StoreMissingKey(t *testing.T) {
	ctx := context.Background()
	st, _ := newTestS3Store(t, "")
	if _, err := st.Get(ctx, "nope.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get = %v", err)
	}
	if err := st.Delete(ctx, "nope.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Delete = %v", err)
	}
	if _, err := st.Stat(ctx, "nope.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat = %v", err)
	}
	if _, _, err := st.Open(ctx, "nope.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open = %v", err)
	}
	if _, err := st.Put(ctx, "../escape", strings.NewReader("x")); err == nil {
		t.Errorf("Put must refuse a climbing key")
	}
}

func TestS3StoreNoNetworkAtConstruction(t *testing.T) {
	st, err := NewS3Store(S3Target{Endpoint: "http://127.0.0.1:1", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}, "/media", nil)
	if err != nil || st == nil {
		t.Fatalf("NewS3Store = %v", err)
	}
}
