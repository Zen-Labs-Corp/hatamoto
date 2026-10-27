package media

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

type changeRepo struct {
	Repository
	m *Media
}

func (r *changeRepo) GetByID(context.Context, uuid.UUID) (*Media, error) { c := *r.m; return &c, nil }
func (r *changeRepo) Update(context.Context, *Media) error               { return nil }
func (r *changeRepo) Delete(context.Context, uuid.UUID) error            { return nil }

// Rendered pages embed alt text, captions and image files, so metadata edits
// and deletes notify the change observer (page-cache purge).
func TestUpdateAndDeleteNotifyChangeObserver(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(&changeRepo{m: &Media{ID: uuid.New(), StoragePath: "x.png"}}, Config{UploadDir: dir}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	calls := 0
	svc.SetChangeObserver(func(context.Context) { calls++ })
	alt := "new alt"
	if _, err := svc.Update(context.Background(), uuid.New(), &UpdateMediaInput{AltText: &alt}); err != nil || calls != 1 {
		t.Fatalf("update: err=%v calls=%d", err, calls)
	}
	if err := svc.Delete(context.Background(), uuid.New()); err != nil || calls != 2 {
		t.Fatalf("delete: err=%v calls=%d", err, calls)
	}
}
