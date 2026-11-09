package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// failUpdateRepo fails UpdateFile on demand.
type failUpdateRepo struct {
	fakeRepo
	fail bool
}

func (r *failUpdateRepo) UpdateFile(ctx context.Context, m *Media) error {
	if r.fail {
		return errors.New("database is down")
	}
	return r.fakeRepo.UpdateFile(ctx, m)
}

func newS3ReplaceEnv(t *testing.T) (*Service, *failUpdateRepo, *s3Env) {
	t.Helper()
	e := newS3Env(t)
	repo := &failUpdateRepo{}
	svc := NewService(repo, Config{UploadDir: e.localDir, BaseURL: "/media", MaxSizeMB: 20, Store: e.svc.store}, slog.Default())
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{WebP: true} })
	return svc, repo, e
}

func readObject(t *testing.T, e *s3Env, key string) []byte {
	t.Helper()
	rc, err := e.svc.store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

func TestS3ReplaceSwapsBytesInPlace(t *testing.T) {
	ctx := context.Background()
	svc, _, e := newS3ReplaceEnv(t)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := len(e.fake.Objects())

	next := jpegPhoto(t, 2500, 1500)
	nm, err := svc.Replace(ctx, m.ID, bytes.NewReader(next), "new.jpg", int64(len(next)))
	if err != nil {
		t.Fatal(err)
	}
	if nm.StoragePath != m.StoragePath || nm.FileSize != int64(len(next)) || *nm.Width != 2500 {
		t.Errorf("replaced row = %+v", nm)
	}
	if !bytes.Equal(readObject(t, e, m.StoragePath), next) {
		t.Error("original object does not hold the new bytes")
	}
	if got := len(e.fake.Objects()); got != before {
		t.Errorf("objects %d -> %d: temporary objects must not remain: %v", before, got, e.fake.Objects())
	}
	for k := range e.fake.Objects() {
		if strings.HasSuffix(k, ".tmp") || strings.HasSuffix(k, ".old") {
			t.Errorf("stray object %s", k)
		}
	}
	e.assertClean(t)
}

func TestS3ReplaceRollsBackWhenTheRowUpdateFails(t *testing.T) {
	ctx := context.Background()
	svc, repo, e := newS3ReplaceEnv(t)
	orig := jpegPhoto(t, 3000, 2000)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(orig), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshotOf := map[string][]byte{}
	for k, o := range e.fake.Objects() {
		snapshotOf[k] = o.Data
	}

	repo.fail = true
	next := jpegPhoto(t, 2000, 1000)
	if _, err := svc.Replace(ctx, m.ID, bytes.NewReader(next), "new.jpg", int64(len(next))); err == nil {
		t.Fatal("expected the update error")
	}
	got := e.fake.Objects()
	if len(got) != len(snapshotOf) {
		t.Errorf("objects after rollback: %d, want %d", len(got), len(snapshotOf))
	}
	for k, want := range snapshotOf {
		if !bytes.Equal(got[k].Data, want) {
			t.Errorf("object %s was not restored", k)
		}
	}
	e.assertClean(t)
}

func TestS3ReplaceRejectsBeforeTouchingTheBucket(t *testing.T) {
	ctx := context.Background()
	svc, _, e := newS3ReplaceEnv(t)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(pngBytes(t, 40, 30)), "a.png", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	orig := readObject(t, e, m.StoragePath)
	svc.config.MaxPixels = 100
	big := pngBytes(t, 60, 60)
	if _, err := svc.Replace(ctx, m.ID, bytes.NewReader(big), "b.png", int64(len(big))); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(readObject(t, e, m.StoragePath), orig) {
		t.Error("a rejected replacement changed the object")
	}
	if left, _ := os.ReadDir(e.localDir); len(left) != 0 {
		t.Errorf("local dir not empty")
	}
	e.assertClean(t)
}
