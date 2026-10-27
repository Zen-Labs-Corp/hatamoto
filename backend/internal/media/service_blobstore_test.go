package media

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"io"
	"path/filepath"
	"sync"
	"testing"
)

// recordingStore wraps a BlobStore and logs the keys written and deleted.
type recordingStore struct {
	BlobStore
	mu      sync.Mutex
	puts    []string
	deletes []string
}

func (r *recordingStore) Put(ctx context.Context, key string, rd io.Reader) (int64, error) {
	r.mu.Lock()
	r.puts = append(r.puts, key)
	r.mu.Unlock()
	return r.BlobStore.Put(ctx, key, rd)
}

func (r *recordingStore) Delete(ctx context.Context, key string) error {
	r.mu.Lock()
	r.deletes = append(r.deletes, key)
	r.mu.Unlock()
	return r.BlobStore.Delete(ctx, key)
}

type memRepo struct {
	fakeRepo
	got *Media
}

func (m *memRepo) GetByID(context.Context, uuid.UUID) (*Media, error) { return m.got, nil }

func TestServiceWritesThroughBlobStore(t *testing.T) {
	svc := newTestService(t, Config{})
	rec := &recordingStore{BlobStore: svc.store}
	svc.store = rec
	repo := &memRepo{}
	svc.repo = repo

	data := pngBytes(t, 1200, 900)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "pic.png", "image/png", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	wantURL := "/media/" + legacyMediaTenantDir + "/" + filepath.Base(m.StoragePath)
	if *m.PublicURL != wantURL {
		t.Errorf("PublicURL = %q, want %q", *m.PublicURL, wantURL)
	}
	if got := svc.store.URL(m.StoragePath); got != *m.PublicURL {
		t.Errorf("store.URL = %q, PublicURL %q", got, *m.PublicURL)
	}
	if len(m.Variants) == 0 {
		t.Fatal("expected variants for a 1200x900 png")
	}
	for _, v := range m.Variants {
		if v.URL != "/media/"+v.Path {
			t.Errorf("variant URL %q for path %q", v.URL, v.Path)
		}
	}
	if len(rec.puts) != 1+len(m.Variants) || rec.puts[0] != m.StoragePath {
		t.Errorf("puts = %v", rec.puts)
	}

	repo.got = m
	if err := svc.Delete(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	if len(rec.deletes) != 1+len(m.Variants) || rec.deletes[0] != m.StoragePath {
		t.Errorf("deletes = %v", rec.deletes)
	}
	if _, err := svc.OpenFile(m.StoragePath); err == nil {
		t.Error("file still present after Delete")
	}
}
