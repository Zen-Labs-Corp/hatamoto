package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/siteconfig"
)

const (
	preloadJobID = "core:preload"
	// minPreloadInterval mirrors the scheduler's minimum dynamic interval.
	minPreloadInterval = time.Minute
	// A content purge schedules a preload this long after the last purge,
	// but never later than defaultChangeMaxDelay after the first one.
	defaultChangeDebounce = 2 * time.Minute
	defaultChangeMaxDelay = 10 * time.Minute
)

// preloadScheduler is the part of worker.Scheduler the service uses.
type preloadScheduler interface {
	Schedule(id string, every time.Duration, fn func(context.Context) error) error
	Unschedule(prefix string)
}

// pageCacheSite is the siteconfig store as the service uses it.
type pageCacheSite interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error)
}

// pageCacheService implements admin.PageCacheService: it persists the
// settings, applies them to the live runtime, cache cap and scheduler, and
// runs clear and preload. Scheduler may be nil (HATAMOTO_WORKER=off).
type pageCacheService struct {
	site       pageCacheSite
	cache      *cache.Cache
	runtime    *pageCacheRuntime
	preloader  *pagePreloader
	invalidate func(context.Context)
	scheduler  preloadScheduler // nil when the worker is off
	serverTTL  time.Duration
	ttlFromEnv bool
	baseURL    func() string
	lifecycle  func() context.Context // nil until Start
	schedMu    sync.Mutex

	// Live preload settings and the debounced after-change trigger.
	missingOnly  atomic.Bool
	afterChanges atomic.Bool
	preloadOn    atomic.Bool
	debounce     time.Duration // 0 means defaultChangeDebounce
	maxDelay     time.Duration // 0 means defaultChangeMaxDelay
	dbMu         sync.Mutex
	dbTimer      *time.Timer
	dbFirst      time.Time
	dbClosed     bool   // set when stopPreload begins; nothing triggers after
	dbGen        uint64 // identifies the live debounce timer

	// loadWG tracks the boot settings retry; retryInitial overrides its first delay (tests).
	loadWG       sync.WaitGroup
	retryInitial time.Duration
}

var _ admin.PageCacheService = (*pageCacheService)(nil)

func (p *pageCacheService) lifecycleCtx() context.Context {
	if p.lifecycle == nil {
		return nil
	}
	return p.lifecycle()
}

func (p *pageCacheService) defaultTTL() time.Duration {
	if p.serverTTL > 0 {
		return p.serverTTL
	}
	return config.DefaultPageCacheTTL
}

// Apply pushes stored settings into the live runtime, the cache cap and the
// scheduler. It reports whether the cache was on before and is on now.
func (p *pageCacheService) Apply(c siteconfig.Config) (wasOn, isOn bool) {
	wasOn = p.runtime.Enabled()
	p.runtime.Apply(c.PageCacheEnabled, time.Duration(c.PageCacheTTLSeconds)*time.Second)
	if p.cache != nil {
		p.cache.SetRenderMaxBytes(int64(c.PageCacheMaxMB) << 20)
	}
	p.missingOnly.Store(c.PagePreloadMode != siteconfig.PreloadModeAll)
	p.afterChanges.Store(c.PagePreloadAfterChanges)
	p.preloadOn.Store(c.PagePreloadEnabled)
	if !c.PageCacheEnabled || !c.PagePreloadEnabled || !c.PagePreloadAfterChanges {
		p.cancelPendingPreload()
	}
	p.reschedule(c)
	return wasOn, c.PageCacheEnabled
}

// reschedule sets or removes the preload job to match c. The job only
// triggers the single-flight preloader and does nothing when the cache is off.
func (p *pageCacheService) reschedule(c siteconfig.Config) {
	if p.scheduler == nil {
		return
	}
	p.schedMu.Lock()
	defer p.schedMu.Unlock()
	if !c.PageCacheEnabled || !c.PagePreloadEnabled {
		p.scheduler.Unschedule(preloadJobID)
		return
	}
	every := time.Duration(c.PagePreloadIntervalSeconds) * time.Second
	if every <= 0 {
		every, _ = resolveTTL(c.PageCacheTTLSeconds, p.defaultTTL())
	}
	if every < minPreloadInterval {
		every = minPreloadInterval
	}
	_ = p.scheduler.Schedule(preloadJobID, every, func(context.Context) error {
		if p.runtime.Enabled() {
			p.preloader.Trigger(p.lifecycleCtx())
		}
		return nil
	})
}

// notifyContentChanged schedules a debounced preload after a content purge.
// Nil-safe; does nothing unless the cache, preload and after-changes are on.
func (p *pageCacheService) notifyContentChanged() {
	if p == nil || p.runtime == nil || !p.runtime.Enabled() || !p.preloadOn.Load() || !p.afterChanges.Load() {
		return
	}
	if p.baseURL == nil || p.baseURL() == "" {
		return
	}
	window, maxDelay := p.debounce, p.maxDelay
	if window <= 0 {
		window = defaultChangeDebounce
	}
	if maxDelay <= 0 {
		maxDelay = defaultChangeMaxDelay
	}
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	if p.dbClosed {
		return
	}
	now := time.Now()
	if p.dbTimer == nil {
		p.dbFirst = now
	} else {
		p.dbTimer.Stop()
	}
	delay := min(window, p.dbFirst.Add(maxDelay).Sub(now))
	p.armDebounce(delay)
}

// armDebounce starts a timer for the current generation. dbMu must be held.
func (p *pageCacheService) armDebounce(d time.Duration) {
	p.dbGen++
	gen := p.dbGen
	p.dbTimer = time.AfterFunc(max(d, 0), func() { p.firePreload(gen) })
}

// firePreload runs when the debounce window closes. A run still in progress
// would miss the latest change, so it re-arms instead of being dropped. gen
// identifies the timer that fired: a timer superseded while it waited for the
// lock does nothing, so it cannot orphan its successor. The closed check and
// Trigger share dbMu (Trigger does not block), so no run starts once
// stopPreload has begun.
func (p *pageCacheService) firePreload(gen uint64) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	if gen != p.dbGen {
		return
	}
	p.dbTimer = nil
	if p.dbClosed || !p.runtime.Enabled() || !p.preloadOn.Load() || !p.afterChanges.Load() {
		return
	}
	if p.preloader.Trigger(p.lifecycleCtx()) {
		return
	}
	if p.preloader.Status().Running {
		window := p.debounce
		if window <= 0 {
			window = defaultChangeDebounce
		}
		p.dbFirst = time.Now()
		p.armDebounce(window)
	}
}

// cancelPendingPreload drops a scheduled after-change preload.
func (p *pageCacheService) cancelPendingPreload() {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	p.dbGen++
	if p.dbTimer != nil {
		p.dbTimer.Stop()
		p.dbTimer = nil
	}
}

// closeDebounce cancels the pending preload for good (shutdown).
func (p *pageCacheService) closeDebounce() {
	p.dbMu.Lock()
	p.dbClosed = true
	p.dbMu.Unlock()
	p.cancelPendingPreload()
}

func (p *pageCacheService) Stats(ctx context.Context) admin.PageCacheStats {
	st := admin.PageCacheStats{
		ServerDefaultTTL:     p.defaultTTL(),
		ServerDefaultFromEnv: p.ttlFromEnv,
		SchedulerAvailable:   p.scheduler != nil,
		BaseURLPinned:        p.baseURL != nil && p.baseURL() != "",
	}
	if p.runtime.Enabled() && p.cache != nil {
		st.Pages, st.Bytes = p.cache.RenderStats()
	}
	if p.cache != nil {
		st.CapBytes = p.cache.RenderMaxBytes()
	}
	ttl, source := resolveTTL(int(p.runtime.TTL()/time.Second), p.defaultTTL())
	st.EffectiveTTL, st.TTLSource = ttl, source
	return st
}

func (p *pageCacheService) Settings(ctx context.Context) (admin.PageCacheSettings, error) {
	c, err := p.site.Get(ctx)
	if err != nil {
		return admin.PageCacheSettings{}, err
	}
	return admin.PageCacheSettings{
		Enabled: c.PageCacheEnabled, TTLSeconds: c.PageCacheTTLSeconds, MaxMB: c.PageCacheMaxMB,
		PreloadEnabled: c.PagePreloadEnabled, PreloadIntervalSeconds: c.PagePreloadIntervalSeconds,
		PreloadMode: c.PagePreloadMode, PreloadAfterChanges: c.PagePreloadAfterChanges,
	}, nil
}

func (p *pageCacheService) Save(ctx context.Context, in admin.PageCacheSettings) error {
	var mode *string // empty keeps the stored mode
	if in.PreloadMode != "" {
		mode = &in.PreloadMode
	}
	updated, err := p.site.Update(ctx, siteconfig.UpdateInput{
		PageCacheEnabled:           &in.Enabled,
		PageCacheTTLSeconds:        &in.TTLSeconds,
		PageCacheMaxMB:             &in.MaxMB,
		PagePreloadEnabled:         &in.PreloadEnabled,
		PagePreloadIntervalSeconds: &in.PreloadIntervalSeconds,
		PagePreloadMode:            mode,
		PagePreloadAfterChanges:    &in.PreloadAfterChanges,
	})
	if err != nil {
		return err
	}
	wasOn, isOn := p.Apply(*updated)
	if wasOn && !isOn {
		p.preloader.Cancel()
		if p.invalidate != nil {
			p.invalidate(ctx)
		}
	}
	return nil
}

func (p *pageCacheService) Clear(ctx context.Context) (int, error) {
	pages := 0
	if p.cache != nil {
		pages, _ = p.cache.RenderStats()
	}
	if p.invalidate != nil {
		p.invalidate(ctx)
	}
	return pages, nil
}

func (p *pageCacheService) PreloadNow(ctx context.Context) (bool, error) {
	if !p.runtime.Enabled() {
		return false, errors.New("the page cache is off")
	}
	if p.baseURL == nil || p.baseURL() == "" {
		return false, errors.New("PUBLIC_BASE_URL is not set, so nothing is cached")
	}
	return p.preloader.Trigger(p.lifecycleCtx()), nil
}

func (p *pageCacheService) PreloadStatus() admin.PreloadStatus {
	s := p.preloader.Status()
	failures := make([]admin.PreloadFailure, len(s.Failures))
	for i, f := range s.Failures {
		failures[i] = admin.PreloadFailure{Path: f.Path, Reason: f.Reason}
	}
	return admin.PreloadStatus{
		Running: s.Running, Done: s.Done, Total: s.Total, HasResult: s.HasResult,
		Finished: s.Finished, Pages: s.Pages, Errors: s.Errors, Duration: s.Duration,
		StoppedEarly: s.StoppedEarly, Reason: s.Reason, Skipped: s.Skipped,
		Failures: failures,
	}
}

// newPageCacheService wires the service to the server. Handlers and the
// lifecycle context are read lazily: the admin mounts before the render
// handlers exist and the lifecycle context only appears in Start.
func (s *Server) newPageCacheService() *pageCacheService {
	if s.pageRuntime == nil {
		s.pageRuntime = newPageCacheRuntime()
	}
	p := &pageCacheService{
		site:       s.siteConfig,
		cache:      s.infra.Cache,
		runtime:    s.pageRuntime,
		invalidate: s.purgeRendered,
		serverTTL:  s.cfg.PageCacheTTL,
		ttlFromEnv: s.cfg.PageCacheTTLFromEnv,
		baseURL:    s.preloadBase,
		lifecycle:  func() context.Context { return s.lifecycleCtx },
	}
	if s.scheduler != nil {
		p.scheduler = s.scheduler
	}
	p.preloader = &pagePreloader{
		paths:   s.preloadPaths,
		render:  s.preloadRender,
		enabled: s.pageRuntime.Enabled,
		logger:  s.logger,
	}
	p.preloader.missingOnly = p.missingOnly.Load
	if s.infra.Cache != nil {
		p.preloader.evictions = s.infra.Cache.Evictions
		p.preloader.isCached = func(path string) bool { return s.preloadCached(path) }
	}
	return p
}

const (
	loadRetryInitial = 5 * time.Second
	loadRetryMax     = time.Minute
)

// loadPageCache applies the stored settings at boot (and after the
// scheduler started, so the preload job registers). When the first read
// fails it returns at once and retries in the background with a doubling
// backoff until it succeeds or ctx ends, so a database blip at boot does
// not leave the cache running with its defaults.
func (s *Server) loadPageCache(ctx context.Context) {
	if s.pageCache == nil || s.pageCache.site == nil {
		return
	}
	p := s.pageCache
	c, err := p.site.Get(ctx)
	if err == nil {
		p.Apply(*c)
		return
	}
	s.logger.Warn("page cache: load settings, will retry", "error", err)
	initial := p.retryInitial
	if initial <= 0 {
		initial = loadRetryInitial
	}
	p.loadWG.Add(1)
	go func() {
		defer p.loadWG.Done()
		wait := initial
		for {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			c, err := p.site.Get(ctx)
			if err == nil {
				p.Apply(*c)
				s.logger.Info("page cache: settings loaded after retry")
				return
			}
			s.logger.Warn("page cache: load settings, will retry", "error", err)
			wait = min(wait*2, loadRetryMax)
		}
	}()
}

// stopPreload cancels a running preload and waits for it; nil-safe.
func (s *Server) stopPreload() {
	if s.pageCache == nil || s.pageCache.preloader == nil {
		return
	}
	s.pageCache.closeDebounce()
	s.pageCache.preloader.Cancel()
	s.pageCache.preloader.Wait()
	s.pageCache.loadWG.Wait()
}
