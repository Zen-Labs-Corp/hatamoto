package agents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureHandler(t *testing.T, files map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != refreshUserAgent {
			t.Errorf("User-Agent = %q", ua)
		}
		name, ok := files[r.URL.Path]
		switch {
		case !ok:
			http.NotFound(w, r)
		case name == "500":
			w.WriteHeader(http.StatusInternalServerError)
		case name == "huge":
			_, _ = w.Write([]byte(`["10.0.0.0/8"` + strings.Repeat(" ", refreshBodyCap) + `]`))
		default:
			b, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(b)
		}
	})
}

func testRegistry(srv string, keys map[string][]string) *Registry {
	reg := &Registry{IPRangeSources: map[string]RangeSource{}}
	for k, paths := range keys {
		var urls []string
		for _, p := range paths {
			urls = append(urls, srv+p)
		}
		reg.IPRangeSources[k] = RangeSource{URLs: urls, Format: "prefixes"}
	}
	return reg
}

type recordingPersister struct {
	mu    sync.Mutex
	saved map[string][]netip.Prefix
	err   error
}

func (p *recordingPersister) LoadRanges(context.Context) (map[string][]netip.Prefix, error) {
	return nil, nil
}
func (p *recordingPersister) SaveRanges(_ context.Context, key string, pf []netip.Prefix, _ []string, _ time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.saved == nil {
		p.saved = map[string][]netip.Prefix{}
	}
	p.saved[key] = pf
	return p.err
}

func TestRefreshFailedKeyKeepsOldData(t *testing.T) {
	files := map[string]string{"/openai": "openai.json", "/google1": "google.json", "/google2": "500", "/ahrefs": "ahrefs-like.json"}
	srv := httptest.NewServer(fixtureHandler(t, files))
	defer srv.Close()
	reg := testRegistry(srv.URL, map[string][]string{
		"openai": {"/openai"}, "google": {"/google1", "/google2"}, "ahrefs": {"/ahrefs"},
	})
	store := NewRangeStore()
	old := []netip.Prefix{netip.MustParsePrefix("66.249.0.0/16")}
	store.Set("google", old)
	persist := &recordingPersister{}

	rep := NewRefresher(reg, srv.Client(), store, persist).Refresh(context.Background())

	if len(rep.Failed) != 1 || rep.Failed[0].Key != "google" {
		t.Fatalf("failed = %+v, want only google", rep.Failed)
	}
	if len(rep.Refreshed) != 2 || rep.Refreshed[0] != "ahrefs" || rep.Refreshed[1] != "openai" {
		t.Fatalf("refreshed = %v", rep.Refreshed)
	}
	if found, _ := store.Contains([]string{"google"}, netip.MustParseAddr("66.249.1.1")); !found {
		t.Error("google must keep its previous prefixes after a failed fetch")
	}
	if found, _ := store.Contains([]string{"openai"}, netip.MustParseAddr("20.171.206.1")); !found {
		t.Error("openai must be loaded")
	}
	if _, ok := persist.saved["google"]; ok {
		t.Error("a failed key must not be persisted")
	}
	if len(persist.saved["openai"]) != 2 || len(persist.saved["ahrefs"]) != 3 {
		t.Errorf("persisted = %v", persist.saved)
	}
}

func TestRefreshRejectsGarbageOversizeAndNotFound(t *testing.T) {
	files := map[string]string{"/huge": "huge", "/ok": "openai.json"}
	srv := httptest.NewServer(fixtureHandler(t, files))
	defer srv.Close()
	reg := testRegistry(srv.URL, map[string][]string{"huge": {"/huge"}, "missing": {"/nope"}, "ok": {"/ok"}})
	store := NewRangeStore()
	rep := NewRefresher(reg, srv.Client(), store, nil).Refresh(context.Background())
	if len(rep.Failed) != 2 || len(rep.Refreshed) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if store.Has("huge") || store.Has("missing") || !store.Has("ok") {
		t.Error("store state wrong")
	}
}

func TestRefreshPersistFailureIsReportedButMemoryUpdated(t *testing.T) {
	srv := httptest.NewServer(fixtureHandler(t, map[string]string{"/a": "openai.json"}))
	defer srv.Close()
	store := NewRangeStore()
	persist := &recordingPersister{err: errors.New("db down")}
	rep := NewRefresher(testRegistry(srv.URL, map[string][]string{"a": {"/a"}}), srv.Client(), store, persist).Refresh(context.Background())
	if !store.Has("a") || len(rep.Failed) != 1 || len(rep.Refreshed) != 0 {
		t.Errorf("has=%v report=%+v", store.Has("a"), rep)
	}
}

func TestRefreshStopsOnCancel(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`["10.0.0.0/8"]`))
	}))
	defer srv.Close()
	reg := testRegistry(srv.URL, map[string][]string{"a": {"/a"}, "b": {"/b"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := NewRefresher(reg, srv.Client(), NewRangeStore(), nil).Refresh(ctx)
	if len(rep.Refreshed) != 0 || calls != 0 {
		t.Errorf("cancelled refresh did work: %+v calls=%d", rep, calls)
	}
}

func TestRefreshDefaultRegistryKeysHaveURLs(t *testing.T) {
	for k, src := range Default().IPRangeSources {
		if len(src.URLs) == 0 {
			t.Errorf("%s has no urls", k)
		}
	}
}
