package media

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/s3/s3test"
)

// s3Env is a media service whose bytes live in a fake bucket. localDir is the
// upload directory the service must leave empty; spool is the temp directory
// that must be empty after each operation.
type s3Env struct {
	svc      *Service
	repo     *variantRepo
	fake     *s3test.Server
	localDir string
	spool    string
}

func newS3Env(t *testing.T) *s3Env {
	t.Helper()
	spool := t.TempDir()
	t.Setenv("TMPDIR", spool)
	store, fake := newTestS3Store(t, "site/")
	repo := &variantRepo{}
	localDir := t.TempDir()
	svc := NewService(repo, Config{UploadDir: localDir, BaseURL: "/media", MaxSizeMB: 20, Store: store}, slog.Default())
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{WebP: true} })
	return &s3Env{svc: svc, repo: repo, fake: fake, localDir: localDir, spool: spool}
}

func (e *s3Env) assertClean(t *testing.T) {
	t.Helper()
	if left, _ := os.ReadDir(e.localDir); len(left) != 0 {
		t.Errorf("local media dir is not empty: %v", left)
	}
	if left, _ := os.ReadDir(e.spool); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func (e *s3Env) hasObject(key string) bool {
	_, ok := e.fake.Objects()["site/"+key]
	return ok
}

func TestS3UploadVariantsRebuildDelete(t *testing.T) {
	ctx := context.Background()
	e := newS3Env(t)

	m, err := e.svc.Upload(ctx, nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !e.hasObject(m.StoragePath) {
		t.Fatalf("original not in the bucket: %v", e.fake.Objects())
	}
	if m.Width == nil || *m.Width != 3000 {
		t.Errorf("dimensions = %v", m.Width)
	}
	for _, size := range []string{"medium", "large"} {
		fb, w := variantBy(m, size, ""), variantBy(m, size, "image/webp")
		if fb == nil || w == nil {
			t.Fatalf("%s variants missing in %+v", size, m.Variants)
		}
		if !e.hasObject(fb.Path) || !e.hasObject(w.Path) {
			t.Errorf("%s variant objects missing", size)
		}
	}
	if !e.hasObject(variantBy(m, "thumbnail", "").Path) {
		t.Error("thumbnail object missing")
	}
	e.assertClean(t)

	// Rebuild: drop one variant object, rebuild, and it is back.
	thumb := variantBy(m, "thumbnail", "").Path
	if err := e.svc.store.Delete(ctx, thumb); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RegenerateVariants(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if !e.hasObject(thumb) {
		t.Error("rebuild did not rewrite the thumbnail")
	}
	e.assertClean(t)

	if err := e.svc.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	for k := range e.fake.Objects() {
		if strings.HasPrefix(k, "site/") {
			t.Errorf("object left after delete: %s", k)
		}
	}
	e.assertClean(t)
}

func TestS3UploadSmallPNGAndRejects(t *testing.T) {
	ctx := context.Background()
	e := newS3Env(t)
	m, err := e.svc.Upload(ctx, nil, bytes.NewReader(pngBytes(t, 40, 30)), "tiny.png", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !e.hasObject(m.StoragePath) || m.PublicURL == nil || *m.PublicURL != "/media/"+m.StoragePath {
		t.Errorf("stored %v url %v", e.fake.Objects(), m.PublicURL)
	}
	// A pixel-cap rejection removes the object again.
	e.svc.config.MaxPixels = 100
	before := len(e.fake.Objects())
	if _, err := e.svc.Upload(ctx, nil, bytes.NewReader(pngBytes(t, 40, 30)), "big.png", "", 0); err == nil {
		t.Fatal("expected pixel cap error")
	}
	if len(e.fake.Objects()) != before {
		t.Errorf("rejected upload left an object: %v", e.fake.Objects())
	}
	e.assertClean(t)
}

func TestS3UploadRecordsProvider(t *testing.T) {
	e := newS3Env(t)

	m, err := e.svc.Upload(context.Background(), nil, bytes.NewReader(pngBytes(t, 64, 64)), "pic.png", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.StorageProvider != "s3" {
		t.Errorf("returned provider = %q, want s3", m.StorageProvider)
	}
	if got := e.repo.byID[m.ID].StorageProvider; got != "s3" {
		t.Errorf("stored provider = %q, want s3", got)
	}
}
