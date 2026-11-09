package server

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

// A plugin run's upload and import are staged in the bucket and read back
// through a ReaderAt, with nothing left in the local media directory.
func TestS3PluginRunStagingReadsFromTheBucket(t *testing.T) {
	fake := s3test.NewServer(t)
	local, spool := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", spool)
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := media.NewService(nil, media.Config{UploadDir: local, MaxSizeMB: 5, Store: store}, slog.Default())

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key, err := svc.StageBlob(ctx, uuid.New(), bytes.NewReader(buf.Bytes()), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) != 1 {
		t.Fatalf("bucket objects = %v", fake.Objects())
	}
	f, size, err := svc.OpenStaged(key)
	if err != nil || size != int64(buf.Len()) {
		t.Fatalf("OpenStaged = %d, %v", size, err)
	}
	head := make([]byte, 8)
	if _, err := f.ReadAt(head, 0); err != nil || !bytes.Equal(head, buf.Bytes()[:8]) {
		t.Errorf("ReadAt = %x, %v", head, err)
	}
	f.Close()
	if err := svc.DeleteStaged(ctx, []string{key}); err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) != 0 {
		t.Errorf("objects left: %v", fake.Objects())
	}
	for _, dir := range []string{local, spool} {
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("%s not empty: %v", dir, left)
		}
	}
}
