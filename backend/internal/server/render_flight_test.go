package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/render"
)

// flightWaitFor polls cond for up to 5 s.
func flightWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConcurrentMissRendersOnce(t *testing.T) {
	const n = 100
	f := newRenderFixture(t)
	key := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	var loads atomic.Int32
	f.next = func() (*routeResult, pageOutcome, error) {
		loads.Add(1)
		// Hold the leader until every other request is parked on the flight.
		flightWaitFor(t, "followers to park", func() bool { return f.h.flight.waiting(key) == n-1 })
		return okPage("About")()
	}

	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.get("/about")
			codes[i], bodies[i] = w.Code, w.Body.String()
		}()
	}
	wg.Wait()

	if got := loads.Load(); got != 1 {
		t.Fatalf("loads = %d, want 1", got)
	}
	f.mu.Lock()
	resolves := f.resolves
	f.mu.Unlock()
	if resolves != 1 {
		t.Fatalf("renders = %d, want 1", resolves)
	}
	for i := range n {
		if codes[i] != http.StatusOK {
			t.Fatalf("request %d: status %d", i, codes[i])
		}
		if bodies[i] != bodies[0] {
			t.Fatalf("request %d: body differs from request 0", i)
		}
	}
	if len(f.cache.sets) != 1 || f.cache.sets[0] != key {
		t.Fatalf("stores = %v, want one of %q", f.cache.sets, key)
	}
}

func TestFlightFollowersRenderThemselvesWhenLeaderNotStorable(t *testing.T) {
	const n = 5
	f := newRenderFixture(t)
	key := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	build := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, r, rr)
		unc := true
		v.Uncacheable = &unc
		return v, err
	}
	var loads atomic.Int32
	f.next = func() (*routeResult, pageOutcome, error) {
		if loads.Add(1) == 1 {
			flightWaitFor(t, "followers to park", func() bool { return f.h.flight.waiting(key) == n-1 })
		}
		return okPage("About")()
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := f.get("/about"); w.Code != http.StatusOK {
				t.Errorf("status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if got := loads.Load(); got != n {
		t.Fatalf("loads = %d, want %d (each follower renders itself)", got, n)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("uncacheable page stored: %v", f.cache.sets)
	}
}

func TestFlightFollowerCancelDoesNotCancelLeader(t *testing.T) {
	f := newRenderFixture(t)
	key := cache.RenderedPageKey(f.h.engine.Version(), "/about")
	release := make(chan struct{})
	f.next = func() (*routeResult, pageOutcome, error) {
		<-release
		return okPage("About")()
	}
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { leaderDone <- f.get("/about") }()
	flightWaitFor(t, "leader to start", func() bool {
		f.h.flight.mu.Lock()
		defer f.h.flight.mu.Unlock()
		return f.h.flight.calls[key] != nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	followerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		followerDone <- f.get("/about", func(r *http.Request) { *r = *r.WithContext(ctx) })
	}()
	flightWaitFor(t, "follower to park", func() bool { return f.h.flight.waiting(key) == 1 })
	cancel()
	if w := <-followerDone; w.Body.Len() != 0 {
		t.Fatalf("cancelled follower wrote a body: %q", w.Body.String())
	}

	close(release)
	if w := <-leaderDone; w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("leader: status %d, %d bytes", w.Code, w.Body.Len())
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("leader stores = %v, want 1", f.cache.sets)
	}
}

func TestFlightSkipsUncacheableRequests(t *testing.T) {
	const n = 4
	f := newRenderFixture(t)
	var inLoad atomic.Int32
	var overlap atomic.Bool
	f.next = func() (*routeResult, pageOutcome, error) {
		// All n must be inside load at once: none was parked as a follower.
		inLoad.Add(1)
		deadline := time.Now().Add(5 * time.Second)
		for inLoad.Load() < n && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if inLoad.Load() >= n {
			overlap.Store(true)
		}
		return okPage("About")()
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.get("/about?q=1")
		}()
	}
	wg.Wait()
	if !overlap.Load() {
		t.Fatal("uncacheable requests did not render concurrently")
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("uncacheable requests stored: %v", f.cache.sets)
	}
}

func TestRenderFlightFinishIdempotent(t *testing.T) {
	var f renderFlight
	c, leader := f.join("k")
	if !leader {
		t.Fatal("first join must lead")
	}
	fc, follower := f.join("k")
	if follower || fc != c || f.waiting("k") != 1 {
		t.Fatalf("second join: leader=%v waiting=%d", follower, f.waiting("k"))
	}
	got := make(chan flightResult, 1)
	go func() { r, _ := f.wait(context.Background(), fc); got <- r }()

	// An early-return leader finishes empty; a later finish must not override it.
	f.finish("k", c, flightResult{})
	f.finish("k", c, flightResult{body: []byte("late"), stored: true})
	if r := <-got; r.stored || !bytes.Equal(r.body, nil) {
		t.Fatalf("follower saw %+v, want the first (empty) result", r)
	}
	if f.waiting("k") != 0 {
		t.Fatal("finished flight still registered")
	}
	if _, leader := f.join("k"); !leader {
		t.Fatal("join after finish must lead a new flight")
	}
}

func TestFlightFollowerRendersItselfWhenLeaderStalls(t *testing.T) {
	f := newRenderFixture(t)
	f.h.flight.maxWait = 50 * time.Millisecond
	release := make(chan struct{})
	var loads atomic.Int32
	f.next = func() (*routeResult, pageOutcome, error) {
		if loads.Add(1) == 1 {
			<-release // the stalled leader
		}
		return okPage("About")()
	}
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { leaderDone <- f.get("/about") }()
	deadline := time.Now().Add(5 * time.Second)
	for loads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	w := f.get("/about")
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("follower: status %d, %d bytes", w.Code, w.Body.Len())
	}
	if took := time.Since(start); took < 40*time.Millisecond || took > 4*time.Second {
		t.Fatalf("follower returned after %v, want about the 50ms bound", took)
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("loads = %d, want 2 (leader + follower's own render)", got)
	}
	close(release)
	if w := <-leaderDone; w.Code != http.StatusOK {
		t.Fatalf("leader: status %d", w.Code)
	}
}
