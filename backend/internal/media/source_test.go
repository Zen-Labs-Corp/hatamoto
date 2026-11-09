package media

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestWithLocalSource(t *testing.T) {
	ctx := context.Background()
	s3store, _ := newTestS3Store(t, "p/")
	localDir := t.TempDir()
	local, err := NewLocalStore(localDir, "/media")
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]BlobStore{"local": local, "s3": s3store} {
		t.Run(name, func(t *testing.T) {
			spool := t.TempDir()
			t.Setenv("TMPDIR", spool)
			svc := NewService(&fakeRepo{}, Config{UploadDir: localDir, Store: store, MaxTotalBytes: 1}, slog.Default())
			if _, err := store.Put(ctx, "a/b.txt", strings.NewReader("payload")); err != nil {
				t.Fatal(err)
			}
			var seen string
			err := svc.withLocalSource(ctx, "a/b.txt", func(path string) error {
				b, err := os.ReadFile(path)
				seen = string(b)
				return err
			})
			if err != nil || seen != "payload" {
				t.Fatalf("got %q, %v", seen, err)
			}
			if name == "s3" {
				if left, _ := os.ReadDir(spool); len(left) != 0 {
					t.Errorf("temporary copy left behind: %v", left)
				}
				if svc.local != nil || svc.config.MaxTotalBytes != 0 {
					t.Errorf("s3 service must have no local store and no quota")
				}
			}
			err = svc.withLocalSource(ctx, "missing.txt", func(p string) error { _, err := os.ReadFile(p); return err })
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("missing = %v", err)
			}
		})
	}
}
