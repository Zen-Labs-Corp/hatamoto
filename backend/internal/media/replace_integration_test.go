//go:build integration

package media

import (
	"bytes"
	"context"
	"testing"
)

// UpdateFile writes the type, size, dimensions and variants and nothing else.
func TestReplaceUpdatesTheRow(t *testing.T) {
	repo := scratchMediaRepo(t)
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20}, quiet())
	bg := context.Background()
	first := pngBytes(t, 40, 30)
	m, err := svc.Upload(bg, nil, bytes.NewReader(first), "a.png", "", int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	alt := "kept"
	if _, err := svc.Update(bg, m.ID, &UpdateMediaInput{AltText: &alt}); err != nil {
		t.Fatal(err)
	}
	next := pngBytes(t, 640, 480)
	if _, err := svc.Replace(bg, m.ID, bytes.NewReader(next), "b.png", int64(len(next))); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(bg, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FileSize != int64(len(next)) || *got.Width != 640 || *got.Height != 480 ||
		got.AltText == nil || *got.AltText != alt || got.Filename != m.Filename ||
		!got.UpdatedAt.After(m.UpdatedAt) || len(got.Variants) == 0 {
		t.Fatalf("row after replace: %+v", got)
	}
}
