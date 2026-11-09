package changeset

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

// execTx accepts the one INSERT the media writer runs.
type execTx struct{ pgx.Tx }

func (execTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

// TestMediaUndoRemovesObjectsAfterTheRequestIsCancelled: a publish whose
// request goes away rolls back and runs the undo with the cancelled context;
// the attached objects must still be removed from the bucket.
func TestMediaUndoRemovesObjectsAfterTheRequestIsCancelled(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fake := s3test.NewServer(t)
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := media.NewService(nil, media.Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20, Store: store}, slog.Default())

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	key, err := svc.StageBlob(context.Background(), uuid.New(), &buf, "a.png")
	if err != nil {
		t.Fatal(err)
	}
	staged := len(fake.Objects())

	payload := []byte(`{"filename":"a.png"}`)
	out, err := MediaWriter{Svc: svc}.Apply(context.Background(), execTx{}, "owner", Item{Payload: payload, BlobKeys: []string{key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) <= staged {
		t.Fatal("attach stored nothing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out.Undo(ctx)
	if got := len(fake.Objects()); got != staged {
		t.Errorf("%d objects after undo, want only the %d staged", got, staged)
	}
}
