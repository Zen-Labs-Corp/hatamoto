package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/render"
)

func newTestPreloader(paths []string, render func(ctx context.Context, path string) (bool, error)) *pagePreloader {
	return &pagePreloader{
		paths:   func(context.Context) ([]string, error) { return paths, nil },
		render:  render,
		enabled: func() bool { return true },
	}
}

func waitIdle(t *testing.T, p *pagePreloader) {
	t.Helper()
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("preload did not finish")
	}
}

func TestPreloadSingleFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	p := newTestPreloader([]string{"/a"}, func(context.Context, string) (bool, error) {
		started <- struct{}{}
		<-release
		return true, nil
	})
	if !p.Trigger(context.Background()) {
		t.Fatal("first Trigger should start")
	}
	<-started
	if !p.Status().Running {
		t.Fatal("status should be running")
	}
	if p.Trigger(context.Background()) {
		t.Fatal("second Trigger while running must return false")
	}
	close(release)
	waitIdle(t, p)
	if p.Status().Running {
		t.Fatal("status should be idle after the run")
	}
	//lint:ignore SA1012 a nil parent is supported on purpose
	if !p.Trigger(nil) {
		t.Fatal("Trigger after completion should start")
	}
	waitIdle(t, p)
}

func TestPreloadDisabledDoesNotStart(t *testing.T) {
	p := newTestPreloader([]string{"/a"}, func(context.Context, string) (bool, error) { return true, nil })
	p.enabled = func() bool { return false }
	if p.Trigger(context.Background()) {
		t.Fatal("Trigger must be false when the cache is off")
	}
	if p.Status().HasResult {
		t.Fatal("nothing should have run")
	}
}

func TestPreloadCancelStopsRun(t *testing.T) {
	var rendered atomic.Int32
	p := newTestPreloader(make([]string, 100), func(ctx context.Context, _ string) (bool, error) {
		rendered.Add(1)
		<-ctx.Done()
		return false, ctx.Err()
	})
	p.Trigger(context.Background())
	for rendered.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	p.Cancel()
	waitIdle(t, p)
	st := p.Status()
	if !st.StoppedEarly || st.Reason != preloadStopCancelled || st.Done >= st.Total {
		t.Fatalf("cancelled status = %+v", st)
	}
}

func TestPreloadStopsWhenCacheCapEvicts(t *testing.T) {
	var ev atomic.Uint64
	paths := make([]string, 20)
	p := newTestPreloader(paths, func(context.Context, string) (bool, error) {
		ev.Add(1) // every render evicts something
		return true, nil
	})
	p.evictions = ev.Load
	p.concurrency = 1
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	if !st.StoppedEarly || st.Reason != preloadStopSizeLimit {
		t.Fatalf("status = %+v, want size limit stop", st)
	}
	if st.Done >= st.Total {
		t.Fatalf("Done %d should be below Total %d", st.Done, st.Total)
	}
}

func TestPreloadCountsPagesErrorsAndDuration(t *testing.T) {
	var mu sync.Mutex
	tick := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	calls := 0
	p := newTestPreloader([]string{"/ok", "/err", "/private"}, func(_ context.Context, path string) (bool, error) {
		switch path {
		case "/ok":
			return true, nil
		case "/err":
			return false, context.DeadlineExceeded
		}
		return false, nil
	})
	p.concurrency = 1
	p.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 2 {
			tick = tick.Add(64 * time.Second)
		}
		return tick
	}
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	if st.Pages != 1 || st.Errors != 1 || st.Duration != 64*time.Second || st.Total != 3 || st.Done != 3 || !st.HasResult || st.StoppedEarly {
		t.Fatalf("status = %+v", st)
	}
}

func TestPreloadPathsFromLocs(t *testing.T) {
	base := "https://site.example"
	locs := []string{
		base,
		base + "/",
		base + "/about",
		base + "/about", // dupe
		base + "/post?page=2",
		"https://other.example/about",
		"https://site.example.evil.example/x",
		base + "/plugin-page",
		base + "/a%20b",
		"://bad",
	}
	got := preloadPathsFromLocs(base, locs)
	want := []string{"/", "/about", "/plugin-page", "/a%20b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func preloadServer(f *renderFixture) *Server {
	return &Server{
		cfg:            &config.Config{PublicSite: config.PublicSiteConfig{BaseURL: "https://x.io"}},
		renderHandlers: f.h,
	}
}

func TestPreloadRenderOverwritesAndSkipsRead(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.get("/about")
	key := f.cache.sets[0]
	f.cache.data[key] = []byte("stale")

	s := preloadServer(f)
	cached, err := s.preloadRender(context.Background(), "/about")
	if err != nil || !cached {
		t.Fatalf("preloadRender = %v, %v", cached, err)
	}
	if string(f.cache.data[key]) == "stale" || len(f.cache.sets) != 2 {
		t.Fatalf("entry not overwritten, sets=%v", f.cache.sets)
	}
}

func TestPreloadRenderDoesNotLog404AndLeavesSharedHandlers(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	s := preloadServer(f)

	if _, err := s.preloadRender(context.Background(), "/missing"); err == nil {
		t.Fatal("a 404 should be reported as an error")
	}
	if len(f.notFound) != 0 {
		t.Fatalf("preload logged 404s: %v", f.notFound)
	}
	if f.h.log404 == nil {
		t.Fatal("shared handlers must keep their 404 logger")
	}
	f.get("/missing")
	if len(f.notFound) != 1 {
		t.Fatalf("public 404 logging must still work, got %v", f.notFound)
	}
}

func TestPreloadRenderNeedsPinnedBaseURLAndHandlers(t *testing.T) {
	if _, err := (&Server{}).preloadRender(context.Background(), "/"); err == nil {
		t.Error("no handlers: want error")
	}
	f := newRenderFixture(t)
	s := &Server{cfg: &config.Config{}, renderHandlers: f.h}
	if _, err := s.preloadRender(context.Background(), "/"); err == nil {
		t.Error("no base URL: want error")
	}
	if _, err := s.preloadPaths(context.Background()); err == nil {
		t.Error("no base URL: preloadPaths want error")
	}
}

func TestPreloadRecoversFromRenderPanic(t *testing.T) {
	p := newTestPreloader([]string{"/a", "/boom", "/b"}, func(_ context.Context, path string) (bool, error) {
		if path == "/boom" {
			panic("template exploded")
		}
		return true, nil
	})
	var logs bytes.Buffer
	p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	p.concurrency = 1
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	if st.Pages != 2 || st.Errors != 1 || st.Done != 3 || !st.HasResult {
		t.Fatalf("status = %+v", st)
	}
	if out := logs.String(); !strings.Contains(out, "/boom") || !strings.Contains(out, "template exploded") || !strings.Contains(out, "level=ERROR") {
		t.Fatalf("log = %q", out)
	}
	out := logs.String()
	if n := strings.Count(out, "/boom"); n != 1 || strings.Contains(out, "level=WARN") || !strings.Contains(out, "stack=") {
		t.Fatalf("panic must log once at Error with a stack, got %d mentions: %q", n, out)
	}
}

func TestPreloadCapsFailureWarnings(t *testing.T) {
	var paths []string
	for i := range 25 {
		paths = append(paths, fmt.Sprintf("/p%d", i))
	}
	p := newTestPreloader(paths, func(context.Context, string) (bool, error) { return false, errors.New("404") })
	var logs bytes.Buffer
	p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	p.Trigger(context.Background())
	waitIdle(t, p)
	out := logs.String()
	if n := strings.Count(out, "preload render failed"); n != 10 {
		t.Fatalf("per-URL warnings = %d, want 10", n)
	}
	if !strings.Contains(out, "15 more preload failures suppressed") {
		t.Fatalf("missing summary: %q", out)
	}
	if st := p.Status(); st.Errors != 25 {
		t.Fatalf("errors = %d, want all 25 counted", st.Errors)
	}
}

func TestPreloadMissingSkipsCachedAndAllOverwrites(t *testing.T) {
	cached := map[string]bool{"/a": true, "/b": true}
	var rendered sync.Map
	mk := func(missing bool) *pagePreloader {
		p := newTestPreloader([]string{"/a", "/b", "/c"}, func(_ context.Context, path string) (bool, error) {
			rendered.Store(path, true)
			return true, nil
		})
		p.missingOnly = func() bool { return missing }
		p.isCached = func(path string) bool { return cached[path] }
		return p
	}
	p := mk(true)
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	if st.Pages != 1 || st.Skipped != 2 || st.Done != 3 || st.Errors != 0 {
		t.Fatalf("missing: %+v", st)
	}
	if _, ok := rendered.Load("/a"); ok {
		t.Fatal("cached /a was rendered in missing mode")
	}
	rendered = sync.Map{}
	p = mk(false)
	p.Trigger(context.Background())
	waitIdle(t, p)
	st = p.Status()
	if st.Pages != 3 || st.Skipped != 0 {
		t.Fatalf("all: %+v", st)
	}
	if _, ok := rendered.Load("/a"); !ok {
		t.Fatal("all mode must re-render cached /a")
	}
}

func TestPreloadFailuresNamedWithReasons(t *testing.T) {
	p := newTestPreloader([]string{"/ok", "/x", "/boom", "/r", "/long"}, func(_ context.Context, path string) (bool, error) {
		switch path {
		case "/x":
			return false, errors.New("500")
		case "/boom":
			panic("kaput")
		case "/r":
			return false, errors.New("redirect 301")
		case "/long":
			return false, errors.New(strings.Repeat("z", 200))
		}
		return true, nil
	})
	p.concurrency = 1
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	want := []preloadFailure{{"/x", "500"}, {"/boom", "panic"}, {"/r", "redirect 301"}, {"/long", strings.Repeat("z", 80)}}
	if st.Errors != 4 || !reflect.DeepEqual(st.Failures, want) {
		t.Fatalf("errors=%d failures=%+v", st.Errors, st.Failures)
	}
}

func TestPreloadFailuresCapped(t *testing.T) {
	paths := make([]string, 30)
	for i := range paths {
		paths[i] = fmt.Sprintf("/p%d", i)
	}
	p := newTestPreloader(paths, func(context.Context, string) (bool, error) { return false, errors.New("500") })
	p.concurrency = 1
	p.Trigger(context.Background())
	waitIdle(t, p)
	st := p.Status()
	if st.Errors != 30 || len(st.Failures) != maxFailures {
		t.Fatalf("errors=%d kept=%d", st.Errors, len(st.Failures))
	}
}

func TestPreloadCachedMatchesServeKeyForEncodedPath(t *testing.T) {
	f := newRenderFixture(t)
	f.next = okPage("Cafe")
	c := cache.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.h.cache = c
	s := preloadServer(f)
	s.infra = &Infrastructure{Cache: c}
	s.renderEngine = f.h.engine.(*render.Engine)

	for _, path := range []string{"/caf%C3%A9", "/about/"} {
		if s.preloadCached(path) {
			t.Fatalf("%s reported cached before render", path)
		}
		if _, err := s.preloadRender(context.Background(), path); err != nil {
			t.Fatalf("preloadRender %s: %v", path, err)
		}
		if !s.preloadCached(path) {
			t.Errorf("%s not found by preloadCached after preloadRender", path)
		}
	}
}
