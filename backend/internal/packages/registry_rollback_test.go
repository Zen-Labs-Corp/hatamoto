package packages

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// memSeen is an in-memory IndexSeen.
type memSeen struct {
	mu  sync.Mutex
	m   map[string]time.Time
	err error
}

func (s *memSeen) AdvanceIssued(_ context.Context, url string, issued time.Time) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return time.Time{}, s.err
	}
	if s.m == nil {
		s.m = map[string]time.Time{}
	}
	if cur, ok := s.m[url]; !ok || issued.After(cur) {
		s.m[url] = issued
	}
	return s.m[url], nil
}

// signedAt signs an index issued at the given time, valid for 30 days after now.
func signedAt(t *testing.T, key Key, issued time.Time) []byte {
	t.Helper()
	return signedIndex(t, key, func(i *Index) {
		i.IssuedAt = issued.UTC().Format(time.RFC3339)
		i.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	})
}

// serveSequence serves docs in order, repeating the last.
func serveSequence(t *testing.T, docs ...[]byte) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		d := docs[min(n, len(docs)-1)]
		n++
		mu.Unlock()
		_, _ = w.Write(d)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistryFetchRefusesOlderIndex(t *testing.T) {
	key := newKey(t)
	fresh := signedAt(t, key, time.Now().Add(-time.Minute))
	old := signedAt(t, key, time.Now().Add(-60*24*time.Hour))
	reg := func(srv *httptest.Server, seen IndexSeen) *Registry {
		return &Registry{URL: srv.URL, Client: srv.Client(), Trust: trust(t, key), AllowInsecure: true, Seen: seen}
	}
	ctx := context.Background()

	t.Run("older is refused", func(t *testing.T) {
		r := reg(serveSequence(t, fresh, old), &memSeen{})
		if _, err := r.Fetch(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Fetch(ctx); !errors.Is(err, ErrIndexRolledBack) {
			t.Fatalf("err = %v, want ErrIndexRolledBack", err)
		}
	})
	t.Run("no store keeps no record", func(t *testing.T) {
		r := reg(serveSequence(t, fresh, old), nil)
		for i := 0; i < 2; i++ {
			if _, err := r.Fetch(ctx); err != nil {
				t.Fatalf("fetch %d: %v", i, err)
			}
		}
	})
	t.Run("equal issued_at is accepted", func(t *testing.T) {
		r := reg(serveSequence(t, fresh, fresh), &memSeen{})
		for i := 0; i < 2; i++ {
			if _, err := r.Fetch(ctx); err != nil {
				t.Fatalf("fetch %d: %v", i, err)
			}
		}
	})
	t.Run("store error fails closed", func(t *testing.T) {
		boom := errors.New("store down")
		r := reg(serveSequence(t, fresh), &memSeen{err: boom})
		if _, err := r.Fetch(ctx); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the store error", err)
		}
	})
}

func TestNewManagerWiresIndexSeenOnlyWithPool(t *testing.T) {
	r, d := &Registry{URL: "https://example.org/r.json"}, &Registry{URL: "https://example.org/d.json"}
	NewManager(Options{Registry: r, DeveloperRegistry: d})
	if r.Seen != nil || d.Seen != nil {
		t.Fatalf("Seen must stay nil without a pool: %v %v", r.Seen, d.Seen)
	}
}
