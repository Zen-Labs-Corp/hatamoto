package indexnow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/jsonx"
)

type fakeRes struct {
	cfg  Config
	on   bool
	urls func(Item) []string
}

func (f fakeRes) Config(context.Context) (Config, bool) { return f.cfg, f.on }
func (f fakeRes) URLs(_ context.Context, it Item) []string {
	return f.urls(it)
}

type capture struct {
	mu   sync.Mutex
	reqs []payload
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var p payload
	_ = jsonx.Unmarshal(b, &p)
	c.mu.Lock()
	c.reqs = append(c.reqs, p)
	c.mu.Unlock()
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func setup(t *testing.T, res fakeRes, logw io.Writer) (*Runner, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	t.Cleanup(srv.Close)
	log := slog.New(slog.NewTextHandler(logw, nil))
	r := New(res, log, Options{
		Client:      srv.Client(),
		EndpointURL: func(string) string { return srv.URL + "/indexnow" },
	})
	return r, c
}

func cfg() Config {
	return Config{Key: "test-key-for-unit", Endpoint: "www.bing.com", Base: "https://example.com"}
}

func TestFlushBatchesAtCap(t *testing.T) {
	var logs strings.Builder
	res := fakeRes{cfg: cfg(), on: true, urls: func(it Item) []string {
		out := make([]string, 0, len(it.IDs))
		for i := range it.IDs {
			out = append(out, fmt.Sprintf("https://example.com/p/%d", i))
		}
		return out
	}}
	r, c := setup(t, res, &logs)
	r.Notify(Item{Kind: "import", IDs: make([]uuid.UUID, 25_000)})
	r.Flush(context.Background())
	if c.count() != 3 {
		t.Fatalf("requests = %d, want 3", c.count())
	}
	if len(c.reqs[0].URLList) != BatchCap || len(c.reqs[2].URLList) != 5000 {
		t.Fatalf("batch sizes %d/%d", len(c.reqs[0].URLList), len(c.reqs[2].URLList))
	}
	p := c.reqs[0]
	if p.Host != "example.com" || p.KeyLocation != "https://example.com/test-key-for-unit.txt" {
		t.Fatalf("payload = %+v", p)
	}
	if !strings.Contains(logs.String(), "indexnow: submitted") {
		t.Fatalf("log = %q", logs.String())
	}
	if strings.Contains(logs.String(), "test-key-for-unit") {
		t.Fatal("key leaked into the log")
	}
}

func TestDisabledSendsNothing(t *testing.T) {
	res := fakeRes{cfg: cfg(), on: false, urls: func(Item) []string { return []string{"https://example.com/a"} }}
	r, c := setup(t, res, io.Discard)
	r.Notify(Item{Kind: "published"})
	r.Flush(context.Background())
	if c.count() != 0 {
		t.Fatalf("requests = %d, want 0", c.count())
	}
}

func TestDedupeAndForeignHostDropped(t *testing.T) {
	res := fakeRes{cfg: cfg(), on: true, urls: func(Item) []string {
		return []string{"https://example.com/a", "https://example.com/a", "https://other.test/b"}
	}}
	r, c := setup(t, res, io.Discard)
	r.Notify(Item{})
	r.Notify(Item{})
	r.Flush(context.Background())
	if c.count() != 1 || len(c.reqs[0].URLList) != 1 {
		t.Fatalf("reqs = %+v", c.reqs)
	}
}

func TestNotifyNeverBlocksWhenFull(t *testing.T) {
	r, _ := setup(t, fakeRes{}, io.Discard)
	done := make(chan struct{})
	go func() {
		for range queueCap + 50 {
			r.Notify(Item{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked")
	}
}

func TestRunDebouncesAndFlushesOnStop(t *testing.T) {
	res := fakeRes{cfg: cfg(), on: true, urls: func(Item) []string { return []string{"https://example.com/a"} }}
	r, c := setup(t, res, io.Discard)
	r.opt.Debounce = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	for range 3 {
		r.Notify(Item{})
	}
	time.Sleep(50 * time.Millisecond)
	if c.count() != 0 {
		t.Fatal("flushed before debounce")
	}
	cancel()
	r.Wait(context.Background())
	if c.count() != 1 {
		t.Fatalf("requests after stop = %d, want 1", c.count())
	}
}

func TestRunFlushesAtMaxPending(t *testing.T) {
	res := fakeRes{cfg: cfg(), on: true, urls: func(Item) []string { return []string{"https://example.com/a"} }}
	r, c := setup(t, res, io.Discard)
	r.opt.Debounce = time.Hour
	r.opt.MaxPending = 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	for range 5 {
		r.Notify(Item{})
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.count() != 1 {
		t.Fatalf("requests = %d, want 1", c.count())
	}
}
