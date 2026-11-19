package media

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestBuildVariants_EnforcesPixelCapBeforeDecode(t *testing.T) {
	repo := &variantRepo{}
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20, MaxPixels: 1500}, slog.Default())

	// The header declares 100x100 (over the cap) but carries no real bitstream:
	// only a size check ahead of the decoder yields ErrImageTooLarge.
	rel := filepath.Join(legacyMediaTenantDir, "b.webp")
	full := svc.local.localPath(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, buildVP8WebP(t, 100, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	existing := []ImageVariant{{Size: "thumbnail", Path: "keep.webp", Width: 10, Height: 10}}
	repo.byID = map[uuid.UUID]*Media{id: {ID: id, Filename: "b.webp", StoragePath: rel, MimeType: "image/webp", Variants: existing}}

	if _, err := svc.RegenerateVariants(context.Background(), id); err != ErrImageTooLarge {
		t.Fatalf("got %v, want ErrImageTooLarge", err)
	}
	if !reflect.DeepEqual(repo.byID[id].Variants, existing) {
		t.Errorf("stored variants changed: %+v", repo.byID[id].Variants)
	}
}
