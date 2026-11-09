package portability

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

func s3StoreAt(t *testing.T, fake *s3test.Server, prefix string) *media.S3Store {
	t.Helper()
	st, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: prefix, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A media file travels from one prefix to another through the export reader
// and the import stage; the local media directory is never used and the
// operating system's temporary files are gone afterwards.
func TestMediaStoreExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	spool := t.TempDir()
	t.Setenv("TMPDIR", spool)
	fake := s3test.NewServer(t)
	src, dst := s3StoreAt(t, fake, "a/"), s3StoreAt(t, fake, "b/")
	if _, err := src.Put(ctx, "2026/pic.png", strings.NewReader(pngMagic+"body")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	size, err := addStoreMediaFile(ctx, tw, src, "2026/pic.png", time.Unix(0, 0))
	if err != nil || size != int64(len(pngMagic+"body")) {
		t.Fatalf("export = %d, %v", size, err)
	}
	if size, err := addStoreMediaFile(ctx, tw, src, "2026/gone.png", time.Unix(0, 0)); err != nil || size != -1 {
		t.Fatalf("missing object = %d, %v; want -1, nil", size, err)
	}
	tw.Close()

	tr := tar.NewReader(&buf)
	h, err := tr.Next()
	if err != nil || h.Name != mediaPrefix+"2026/pic.png" {
		t.Fatalf("entry = %+v, %v", h, err)
	}
	localDir := t.TempDir()
	st := &mediaStage{root: localDir, store: dst, ctx: ctx}
	if n, _, err := st.add(strings.TrimPrefix(h.Name, mediaPrefix), tr); err != nil || n != size {
		t.Fatalf("stage = %d, %v", n, err)
	}
	if err := st.place(); err != nil {
		t.Fatal(err)
	}
	st.cleanup()
	if _, ok := fake.Objects()["b/2026/pic.png"]; !ok {
		t.Fatalf("not placed: %v", fake.Objects())
	}
	if left, _ := os.ReadDir(localDir); len(left) != 0 {
		t.Errorf("local dir used: %v", left)
	}
	if left, _ := os.ReadDir(spool); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	// The target now has the file: a second import refuses to overwrite it.
	again := &mediaStage{root: localDir, store: dst, ctx: ctx}
	if _, _, err := again.add("2026/pic.png", strings.NewReader(pngMagic)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collision err = %v", err)
	}
	again.cleanup()

	// A failed import removes what this import placed, nothing else.
	if _, err := dst.Put(ctx, "keep.png", strings.NewReader(pngMagic)); err != nil {
		t.Fatal(err)
	}
	st = &mediaStage{root: localDir, store: dst, ctx: ctx}
	if _, _, err := st.add("new.png", strings.NewReader(pngMagic)); err != nil {
		t.Fatal(err)
	}
	if err := st.place(); err != nil {
		t.Fatal(err)
	}
	st.undo()
	st.cleanup()
	objs := fake.Objects()
	if _, ok := objs["b/new.png"]; ok {
		t.Error("placed object survived undo")
	}
	if _, ok := objs["b/keep.png"]; !ok {
		t.Error("undo removed an object it did not place")
	}
}
