package media

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/s3/s3test"
)

func isStagedObject(key string) bool { return strings.Contains(key, "/"+stagedDir+"/") }

// failCreateRepo fails Create, as a database error after the files were stored.
type failCreateRepo struct{ variantRepo }

func (*failCreateRepo) Create(context.Context, *Media) error { return errors.New("database is down") }

// failUpdateVariantRepo fails Update.
type failUpdateVariantRepo struct{ variantRepo }

func (*failUpdateVariantRepo) Update(context.Context, *Media) error {
	return errors.New("database is down")
}

func newCleanupEnv(t *testing.T, repo Repository) (*Service, *s3test.Server) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	store, fake := newTestS3Store(t, "site/")
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20, Store: store}, slog.Default())
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{WebP: true} })
	return svc, fake
}

func assertNoObjects(t *testing.T, fake *s3test.Server) {
	t.Helper()
	if objs := fake.Objects(); len(objs) != 0 {
		keys := make([]string, 0, len(objs))
		for k := range objs {
			keys = append(keys, k)
		}
		t.Errorf("objects left behind: %v", keys)
	}
}

func TestS3UploadWhoseReplyWasLostLeavesNoObject(t *testing.T) {
	svc, fake := newCleanupEnv(t, &variantRepo{})
	fake.PutThenFail(func(string) bool { return true })
	if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(pngBytes(t, 40, 30)), "a.png", "", 0); err == nil {
		t.Fatal("upload succeeded")
	}
	assertNoObjects(t, fake)
}

// TestS3UploadCancelledAfterThePutLandedLeavesNoObject cancels the request
// right after the bucket stored the object, the way a client that goes away
// does: the cleanup must not depend on that context.
func TestS3UploadCancelledAfterThePutLandedLeavesNoObject(t *testing.T) {
	fake := s3test.NewServer(t)
	target, _ := url.Parse(fake.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !once.CompareAndSwap(false, true) {
			proxy.ServeHTTP(w, r)
			return
		}
		// Store the object, then cancel the caller before it hears about it.
		proxy.ServeHTTP(httptest.NewRecorder(), r.WithContext(context.WithoutCancel(r.Context())))
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(front.Close)
	t.Setenv("TMPDIR", t.TempDir())
	store, err := NewS3Store(S3Target{Endpoint: front.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(&variantRepo{}, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20, Store: store}, slog.Default())

	if _, err := svc.Upload(ctx, nil, bytes.NewReader(pngBytes(t, 40, 30)), "a.png", "", 0); err == nil {
		t.Fatal("upload succeeded although its context was cancelled")
	}
	if !once.Load() {
		t.Fatal("the PUT never went through the front")
	}
	assertNoObjects(t, fake)
}

func TestS3UploadVariantWhoseReplyWasLostLeavesNoObject(t *testing.T) {
	svc, fake := newCleanupEnv(t, &variantRepo{})
	fake.PutThenFail(func(key string) bool { return strings.Contains(key, "_") }) // variants only
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Variants) != 0 {
		t.Fatalf("variants = %+v, want none", m.Variants)
	}
	if objs := fake.Objects(); len(objs) != 1 {
		t.Errorf("objects = %d, want only the original", len(objs))
	}
}

func TestS3UploadRowFailureRemovesOriginalAndVariants(t *testing.T) {
	svc, fake := newCleanupEnv(t, &failCreateRepo{})
	if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0); err == nil {
		t.Fatal("upload succeeded")
	}
	assertNoObjects(t, fake)
}

func TestS3StageBlobWhoseReplyWasLostLeavesNoObject(t *testing.T) {
	svc, fake := newCleanupEnv(t, &variantRepo{})
	fake.PutThenFail(func(string) bool { return true })
	if _, err := svc.StageBlob(context.Background(), uuid.New(), bytes.NewReader(pngBytes(t, 40, 30)), "a.png"); err == nil {
		t.Fatal("staging succeeded")
	}
	assertNoObjects(t, fake)
}

func TestS3AttachWhoseReplyWasLostLeavesOnlyTheStagedObject(t *testing.T) {
	ctx := context.Background()
	svc, fake := newCleanupEnv(t, &variantRepo{})
	key, err := svc.StageBlob(ctx, uuid.New(), bytes.NewReader(pngBytes(t, 40, 30)), "a.png")
	if err != nil {
		t.Fatal(err)
	}
	fake.PutThenFail(func(k string) bool { return !isStagedObject(k) })
	if _, err := svc.prepareAttach(ctx, MediaOp{StagedKey: key, Filename: "a.png", Origin: "plug"}); err == nil {
		t.Fatal("attach succeeded")
	}
	if objs := fake.Objects(); len(objs) != 1 || !isStagedObject(firstKey(objs)) {
		t.Errorf("objects = %v, want only the staged one", objs)
	}
}

func TestS3RegenerateVariantsRowFailureRemovesTheNewCopies(t *testing.T) {
	ctx := context.Background()
	repo := &failUpdateVariantRepo{}
	svc, fake := newCleanupEnv(t, repo)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// The row names no copy, and the bucket has none: a rebuild writes new ones.
	for _, v := range m.Variants {
		if err := svc.store.Delete(ctx, v.Path); err != nil {
			t.Fatal(err)
		}
	}
	repo.byID[m.ID].Variants = nil
	if _, err := svc.RegenerateVariants(ctx, m.ID); err == nil {
		t.Fatal("rebuild succeeded")
	}
	if objs := fake.Objects(); len(objs) != 1 {
		t.Errorf("objects = %d, want only the original", len(objs))
	}
}

func firstKey(m map[string]s3test.Object) string {
	for k := range m {
		return k
	}
	return ""
}

// TestS3ReplaceInPlaceKeepsTheOldBytesWhenTheReplyWasLost guards the key that
// must never be cleaned up: an object that exists already.
func TestS3ReplaceInPlaceKeepsTheOldBytesWhenTheReplyWasLost(t *testing.T) {
	ctx := context.Background()
	svc, _, e := newS3ReplaceEnv(t)
	old := pngBytes(t, 40, 30)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(old), "a.png", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var first atomic.Bool
	e.fake.PutThenFail(func(string) bool { return first.CompareAndSwap(false, true) })
	next := pngBytes(t, 50, 40)
	if _, err := svc.Replace(ctx, m.ID, bytes.NewReader(next), "b.png", int64(len(next))); err == nil {
		t.Fatal("replace succeeded")
	}
	if got := readObject(t, e, m.StoragePath); !bytes.Equal(got, old) {
		t.Error("the original object does not hold the old bytes")
	}
}
