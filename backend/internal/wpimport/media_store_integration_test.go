//go:build integration

package wpimport

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

// With a store in DepsConfig the import writes media to the bucket and leaves
// the local media directory empty.
func TestImportMediaGoesToTheStore_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	fake := s3test.NewServer(t)
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "wp/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	d := NewDeps(db, DepsConfig{MediaDir: local, Store: store}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	m, err := d.Media.Upload(ctx, nil, bytes.NewReader(buf.Bytes()), "wp-store.png", "", int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Media.Delete(ctx, m.ID) })
	if _, ok := fake.Objects()["wp/"+m.StoragePath]; !ok {
		t.Errorf("original not in the bucket: %v", fake.Objects())
	}
	if left, _ := os.ReadDir(local); len(left) != 0 {
		t.Errorf("local media dir must stay empty, has %v", left)
	}
}
