package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/siteconfig"
)

type fakePageSite struct {
	cfg siteconfig.Config
}

func (f *fakePageSite) Get(context.Context) (*siteconfig.Config, error) {
	c := f.cfg
	return &c, nil
}

func (f *fakePageSite) Update(_ context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if in.PageCacheEnabled != nil {
		f.cfg.PageCacheEnabled = *in.PageCacheEnabled
	}
	if in.PageCacheTTLSeconds != nil {
		f.cfg.PageCacheTTLSeconds = *in.PageCacheTTLSeconds
	}
	if in.PageCacheMaxMB != nil {
		f.cfg.PageCacheMaxMB = *in.PageCacheMaxMB
	}
	if in.PagePreloadEnabled != nil {
		f.cfg.PagePreloadEnabled = *in.PagePreloadEnabled
	}
	if in.PagePreloadIntervalSeconds != nil {
		f.cfg.PagePreloadIntervalSeconds = *in.PagePreloadIntervalSeconds
	}
	c := f.cfg
	return &c, nil
}

type fakeSched struct {
	mu       sync.Mutex
	jobs     map[string]time.Duration
	fns      map[string]func(context.Context) error
	unscheds int
}

func (f *fakeSched) Schedule(id string, every time.Duration, fn func(context.Context) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		f.jobs, f.fns = map[string]time.Duration{}, map[string]func(context.Context) error{}
	}
	f.jobs[id], f.fns[id] = every, fn
	return nil
}

func (f *fakeSched) Unschedule(prefix string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unscheds++
	delete(f.jobs, prefix)
	delete(f.fns, prefix)
}

type serviceRig struct {
	svc         *pageCacheService
	site        *fakePageSite
	cache       *cache.Cache
	sched       *fakeSched
	invalidated int
	triggered   chan struct{}
}

func newServiceRig(t *testing.T, withScheduler bool, base string) *serviceRig {
	t.Helper()
	r := &serviceRig{
		site:      &fakePageSite{cfg: siteconfig.Config{PageCacheEnabled: true}},
		cache:     cache.New(slog.New(slog.NewTextHandler(io.Discard, nil))),
		sched:     &fakeSched{},
		triggered: make(chan struct{}, 4),
	}
	rt := newPageCacheRuntime()
	r.svc = &pageCacheService{
		site: r.site, cache: r.cache, runtime: rt,
		invalidate: func(context.Context) { r.invalidated++ },
		serverTTL:  10 * time.Minute,
		baseURL:    func() string { return base },
	}
	r.svc.preloader = &pagePreloader{
		paths:   func(context.Context) ([]string, error) { r.triggered <- struct{}{}; return nil, nil },
		render:  func(context.Context, string) (bool, error) { return true, nil },
		enabled: rt.Enabled,
	}
	if withScheduler {
		r.svc.scheduler = r.sched
	}
	return r
}

func TestPageCacheStatsPrecedence(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	st := r.svc.Stats(context.Background())
	if st.EffectiveTTL != 10*time.Minute || st.TTLSource != "server" {
		t.Fatalf("default: %v %q", st.EffectiveTTL, st.TTLSource)
	}
	r.svc.Apply(siteconfig.Config{PageCacheEnabled: true, PageCacheTTLSeconds: 3600})
	st = r.svc.Stats(context.Background())
	if st.EffectiveTTL != time.Hour || st.TTLSource != "admin" {
		t.Fatalf("admin: %v %q", st.EffectiveTTL, st.TTLSource)
	}
	if !st.SchedulerAvailable || !st.BaseURLPinned {
		t.Fatalf("flags: %+v", st)
	}
	r.svc.serverTTL = 0
	r.svc.runtime.Apply(true, 0)
	if got := r.svc.Stats(context.Background()).EffectiveTTL; got != config.DefaultPageCacheTTL {
		t.Fatalf("built-in default: %v", got)
	}
}

func TestPageCacheSaveAppliesLive(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	err := r.svc.Save(context.Background(), admin.PageCacheSettings{
		Enabled: true, TTLSeconds: 3600, MaxMB: 2, PreloadEnabled: true, PreloadIntervalSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.svc.runtime.TTL() != time.Hour {
		t.Fatalf("ttl %v", r.svc.runtime.TTL())
	}
	if r.cache.RenderMaxBytes() != 2<<20 {
		t.Fatalf("cap %d", r.cache.RenderMaxBytes())
	}
	if r.sched.jobs[preloadJobID] != 15*time.Minute {
		t.Fatalf("jobs %v", r.sched.jobs)
	}
	if r.invalidated != 0 {
		t.Fatal("must not invalidate while on")
	}
	got, _ := r.svc.Settings(context.Background())
	if got.TTLSeconds != 3600 || got.MaxMB != 2 || !got.PreloadEnabled {
		t.Fatalf("stored %+v", got)
	}
}

func TestPageCacheScheduleIntervalDefaults(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	r.svc.reschedule(siteconfig.Config{PageCacheEnabled: true, PagePreloadEnabled: true})
	if r.sched.jobs[preloadJobID] != 10*time.Minute {
		t.Fatalf("want server ttl, got %v", r.sched.jobs)
	}
	r.svc.reschedule(siteconfig.Config{PageCacheEnabled: true, PagePreloadEnabled: true, PageCacheTTLSeconds: 30})
	if r.sched.jobs[preloadJobID] != time.Minute {
		t.Fatalf("want 1m floor, got %v", r.sched.jobs)
	}
}

func TestPageCacheSaveOffClearsOnceAndUnschedules(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	ctx := context.Background()
	on := admin.PageCacheSettings{Enabled: true, PreloadEnabled: true, PreloadIntervalSeconds: 900}
	if err := r.svc.Save(ctx, on); err != nil {
		t.Fatal(err)
	}
	off := admin.PageCacheSettings{Enabled: false, PreloadEnabled: true, PreloadIntervalSeconds: 900, MaxMB: 5}
	if err := r.svc.Save(ctx, off); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Save(ctx, off); err != nil {
		t.Fatal(err)
	}
	if r.invalidated != 1 {
		t.Fatalf("invalidated %d times, want once", r.invalidated)
	}
	if _, ok := r.sched.jobs[preloadJobID]; ok {
		t.Fatal("job must be unscheduled while off")
	}
	if got, _ := r.svc.Settings(ctx); !got.PreloadEnabled || got.MaxMB != 5 {
		t.Fatalf("stored values must be kept: %+v", got)
	}
	// Preload off with the cache on also unschedules.
	r.svc.Save(ctx, on)
	r.svc.Save(ctx, admin.PageCacheSettings{Enabled: true})
	if _, ok := r.sched.jobs[preloadJobID]; ok {
		t.Fatal("job must be unscheduled when preload is off")
	}
}

func TestPageCacheScheduledJobTriggersOnlyWhenOn(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	r.svc.reschedule(siteconfig.Config{PageCacheEnabled: true, PagePreloadEnabled: true, PagePreloadIntervalSeconds: 600})
	fn := r.sched.fns[preloadJobID]
	r.svc.runtime.Apply(false, 0)
	if err := fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.svc.preloader.Wait()
	select {
	case <-r.triggered:
		t.Fatal("job ran while cache off")
	default:
	}
	r.svc.runtime.Apply(true, 0)
	_ = fn(context.Background())
	r.svc.preloader.Wait()
	select {
	case <-r.triggered:
	default:
		t.Fatal("job did not trigger a preload")
	}
}

func TestPageCacheNilScheduler(t *testing.T) {
	r := newServiceRig(t, false, "https://x.test")
	if err := r.svc.Save(context.Background(), admin.PageCacheSettings{Enabled: true, PreloadEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if r.svc.Stats(context.Background()).SchedulerAvailable {
		t.Fatal("scheduler should be unavailable")
	}
}

func TestPageCacheClearReturnsPriorCount(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	r.cache.Set(context.Background(), "render:a", []byte("aa"), time.Minute)
	r.cache.Set(context.Background(), "render:b", []byte("bb"), time.Minute)
	n, err := r.svc.Clear(context.Background())
	if err != nil || n != 2 || r.invalidated != 1 {
		t.Fatalf("n=%d err=%v invalidated=%d", n, err, r.invalidated)
	}
}

func TestPageCachePreloadNowGuards(t *testing.T) {
	r := newServiceRig(t, true, "")
	if _, err := r.svc.PreloadNow(context.Background()); err == nil {
		t.Fatal("unpinned base URL must be refused")
	}
	r = newServiceRig(t, true, "https://x.test")
	r.svc.runtime.Apply(false, 0)
	if _, err := r.svc.PreloadNow(context.Background()); err == nil {
		t.Fatal("cache off must be refused")
	}
	r.svc.runtime.Apply(true, 0)
	started, err := r.svc.PreloadNow(context.Background())
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	r.svc.preloader.Wait()
	if !r.svc.PreloadStatus().HasResult {
		t.Fatal("expected a result")
	}
}

func TestStopPreloadNilSafe(t *testing.T) {
	(&Server{}).stopPreload()
	s := &Server{pageCache: &pageCacheService{}}
	s.stopPreload()
	s.loadPageCache(context.Background())
	(&Server{}).loadPageCache(context.TODO())
}

func TestStopPreloadWaitsForRun(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	release := make(chan struct{})
	r.svc.preloader.paths = func(ctx context.Context) ([]string, error) { <-ctx.Done(); <-release; return nil, nil }
	//lint:ignore SA1012 a nil parent is supported on purpose
	r.svc.preloader.Trigger(nil)
	s := &Server{pageCache: r.svc}
	done := make(chan struct{})
	go func() { s.stopPreload(); close(done) }()
	select {
	case <-done:
		t.Fatal("stopPreload returned while a run was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopPreload did not return")
	}
}

type flakyPageSite struct {
	fakePageSite
	mu    sync.Mutex
	fails int
	calls int
}

func (f *flakyPageSite) Get(ctx context.Context) (*siteconfig.Config, error) {
	f.mu.Lock()
	f.calls++
	fail := f.calls <= f.fails
	f.mu.Unlock()
	if fail {
		return nil, errors.New("db down")
	}
	return f.fakePageSite.Get(ctx)
}

func TestLoadPageCacheRetriesUntilSuccess(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	site := &flakyPageSite{fails: 2, fakePageSite: fakePageSite{cfg: siteconfig.Config{PageCacheEnabled: true, PageCacheMaxMB: 7}}}
	r.svc.site = site
	r.svc.retryInitial = time.Millisecond
	r.svc.runtime.Apply(false, 0)
	s := &Server{pageCache: r.svc, logger: slog.New(slog.DiscardHandler)}

	s.loadPageCache(context.Background()) // must not block on the failure
	s.pageCache.loadWG.Wait()

	if !r.svc.runtime.Enabled() || r.cache.RenderMaxBytes() != 7<<20 {
		t.Fatalf("settings not applied after retry: enabled=%v cap=%d", r.svc.runtime.Enabled(), r.cache.RenderMaxBytes())
	}
	if site.calls != 3 {
		t.Fatalf("calls = %d, want 3", site.calls)
	}
}

func TestLoadPageCacheRetryStopsOnShutdown(t *testing.T) {
	r := newServiceRig(t, true, "https://x.test")
	site := &flakyPageSite{fails: 1 << 30}
	r.svc.site = site
	r.svc.retryInitial = time.Millisecond
	r.svc.runtime.Apply(false, 0)
	s := &Server{pageCache: r.svc, logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	s.loadPageCache(ctx)
	cancel()
	done := make(chan struct{})
	go func() { s.stopPreload(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopPreload did not wait out the retry loop")
	}
	if r.svc.runtime.Enabled() {
		t.Fatal("nothing should be applied")
	}
}

func debounceRig(t *testing.T) *serviceRig {
	t.Helper()
	return debounceRigWith(t, 60*time.Millisecond, 200*time.Millisecond)
}

func debounceRigWith(t *testing.T, debounce, maxDelay time.Duration) *serviceRig {
	t.Helper()
	r := newServiceRig(t, true, "https://x.test")
	r.svc.debounce = debounce
	r.svc.maxDelay = maxDelay
	r.svc.Apply(siteconfig.Config{PageCacheEnabled: true, PagePreloadEnabled: true, PagePreloadAfterChanges: true})
	t.Cleanup(r.svc.closeDebounce)
	return r
}

func expectRun(t *testing.T, r *serviceRig, within time.Duration) bool {
	t.Helper()
	select {
	case <-r.triggered:
		r.svc.preloader.Wait()
		return true
	case <-time.After(within):
		return false
	}
}

// The burst is paced by wall-clock sleeps, so the window must be far wider than
// a sleep can overshoot on a loaded CI runner (a gap longer than the window, or
// a burst longer than maxDelay, legitimately splits the burst into two runs).
func TestAfterChangePreloadCoalescesBurst(t *testing.T) {
	r := debounceRigWith(t, 300*time.Millisecond, 5*time.Second)
	s := &Server{pageCache: r.svc}
	for range 5 {
		s.invalidateRendered(context.Background())
		time.Sleep(20 * time.Millisecond)
	}
	if expectRun(t, r, 30*time.Millisecond) {
		t.Fatal("ran before the window closed")
	}
	if !expectRun(t, r, 3*time.Second) {
		t.Fatal("no preload after the burst")
	}
	if expectRun(t, r, 400*time.Millisecond) {
		t.Fatal("burst produced more than one run")
	}
}

// The window slides: every call restarts it, so a burst longer than the window still
// ends in one run, after its last call. With a window of 300ms and calls 150ms apart
// the burst (750ms) is far longer than the window, so a timer that is fixed at the
// first call (or never restarted) fires mid-burst and the check between the calls
// fails. Each gap is half the window, so a sleep overshooting by a lot is needed
// before a correct implementation splits the burst; the failing direction needs no
// timing luck at all.
func TestAfterChangePreloadWindowSlides(t *testing.T) {
	const window, gap, calls = 300 * time.Millisecond, 150 * time.Millisecond, 6
	r := debounceRigWith(t, window, 5*time.Second)
	s := &Server{pageCache: r.svc}
	start := time.Now()
	for i := range calls {
		if i > 0 {
			time.Sleep(gap)
		}
		select {
		case <-r.triggered:
			t.Fatalf("ran %v into the burst (call %d of %d): the window did not slide", time.Since(start).Round(time.Millisecond), i+1, calls)
		default:
		}
		s.invalidateRendered(context.Background())
	}
	if time.Since(start) <= window {
		t.Fatalf("the burst took %v, not longer than the %v window: the test proves nothing", time.Since(start), window)
	}
	if !expectRun(t, r, window+3*time.Second) {
		t.Fatal("no preload after the burst")
	}
	if expectRun(t, r, 400*time.Millisecond) {
		t.Fatal("burst produced more than one run")
	}
}

func TestAfterChangePreloadRespectsMaxDelay(t *testing.T) {
	r := debounceRig(t)
	s := &Server{pageCache: r.svc}
	start := time.Now()
	stop := time.After(600 * time.Millisecond)
	tick := time.NewTicker(20 * time.Millisecond) // keeps pushing the window back
	defer tick.Stop()
	for {
		s.invalidateRendered(context.Background())
		select {
		case <-r.triggered:
			if d := time.Since(start); d > 400*time.Millisecond {
				t.Fatalf("ran after %v, want about max delay 200ms", d)
			}
			return
		case <-stop:
			t.Fatal("max delay did not fire during a continuous burst")
		case <-tick.C:
		}
	}
}

func TestAfterChangePreloadNotTriggeredByClearOffOrDisabled(t *testing.T) {
	r := debounceRig(t)
	ctx := context.Background()
	if _, err := r.svc.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{pageCache: r.svc}
	s.purgeRendered(ctx)
	if expectRun(t, r, 200*time.Millisecond) {
		t.Fatal("Clear/purge must not schedule a preload")
	}
	// Switching the cache off cancels a pending run and does not schedule one.
	s.invalidateRendered(ctx)
	if err := r.svc.Save(ctx, admin.PageCacheSettings{Enabled: false, PreloadEnabled: true, PreloadAfterChanges: true}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRendered(ctx)
	if expectRun(t, r, 200*time.Millisecond) {
		t.Fatal("cache off must not preload")
	}
	// Cache on, after-changes off.
	r.svc.Apply(siteconfig.Config{PageCacheEnabled: true, PagePreloadEnabled: true})
	s.invalidateRendered(ctx)
	if expectRun(t, r, 200*time.Millisecond) {
		t.Fatal("after-changes off must not preload")
	}
	// Preload off.
	r.svc.Apply(siteconfig.Config{PageCacheEnabled: true, PagePreloadAfterChanges: true})
	s.invalidateRendered(ctx)
	if expectRun(t, r, 200*time.Millisecond) {
		t.Fatal("preload off must not preload")
	}
}

func TestAfterChangePreloadCancelledOnShutdown(t *testing.T) {
	r := debounceRig(t)
	s := &Server{pageCache: r.svc}
	s.invalidateRendered(context.Background())
	s.stopPreload()
	if expectRun(t, r, 250*time.Millisecond) {
		t.Fatal("pending preload survived shutdown")
	}
	s.invalidateRendered(context.Background()) // after close: ignored
	if expectRun(t, r, 250*time.Millisecond) {
		t.Fatal("preload scheduled after shutdown")
	}
}

func TestInvalidateRenderedNilSafe(t *testing.T) {
	(&Server{}).invalidateRendered(context.Background())
}

func TestFirePreloadIgnoresSupersededTimer(t *testing.T) {
	r := debounceRig(t)
	r.svc.debounce = time.Hour
	r.svc.notifyContentChanged()
	r.svc.dbMu.Lock()
	stale := r.svc.dbGen
	r.svc.dbMu.Unlock()
	r.svc.notifyContentChanged() // re-arms: the first timer is now superseded

	r.svc.firePreload(stale)

	r.svc.dbMu.Lock()
	kept := r.svc.dbTimer != nil
	r.svc.dbMu.Unlock()
	if !kept {
		t.Fatal("superseded timer cleared the handle of its successor")
	}
	if expectRun(t, r, 50*time.Millisecond) {
		t.Fatal("superseded timer triggered a run")
	}
}

func TestFirePreloadDoesNotTriggerOnceStopping(t *testing.T) {
	r := debounceRig(t)
	r.svc.debounce = time.Hour
	r.svc.notifyContentChanged()
	r.svc.dbMu.Lock()
	gen := r.svc.dbGen
	r.svc.dbMu.Unlock()

	r.svc.closeDebounce() // stopPreload has begun

	r.svc.firePreload(gen)
	if expectRun(t, r, 50*time.Millisecond) {
		t.Fatal("preload started after shutdown began")
	}
}
