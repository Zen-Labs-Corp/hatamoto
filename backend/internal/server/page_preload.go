package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/cache"
)

const (
	preloadConcurrency = 2
	// preloadStopSizeLimit is the Reason when a run ends because the cache cap evicted pages.
	preloadStopSizeLimit = "size limit reached"
	preloadStopCancelled = "cancelled"
	// maxFailureLogs caps the per-URL failure warnings of one run.
	maxFailureLogs = 10
	// maxFailures caps the failures kept in the status; maxReasonLen the reason text.
	maxFailures  = 20
	maxReasonLen = 80
)

// preloadFailure names one page that did not preload and why.
type preloadFailure struct {
	Path   string
	Reason string // "500", "redirect 301", "panic" or a short error text
}

// failureReason turns a render error into the short reason shown to the owner.
func failureReason(err error) string {
	if errors.Is(err, errRenderPanic) {
		return "panic"
	}
	r := err.Error()
	if len(r) > maxReasonLen {
		r = r[:maxReasonLen]
	}
	return r
}

// preloadStatus is a snapshot of the running or last preload.
type preloadStatus struct {
	Running     bool
	Done, Total int

	HasResult    bool
	Finished     time.Time
	Pages        int // pages answered 200 and cacheable
	Skipped      int // pages left alone because they were already cached (missing mode)
	Errors       int
	Failures     []preloadFailure // first maxFailures failures
	Duration     time.Duration
	StoppedEarly bool
	Reason       string
}

// pagePreloader re-renders every public page into the cache. One run at a
// time (single-flight); the dependencies are injected so it tests without a
// database or a router.
type pagePreloader struct {
	paths     func(ctx context.Context) ([]string, error)
	render    func(ctx context.Context, path string) (cached bool, err error)
	evictions func() uint64
	enabled   func() bool
	// missingOnly and isCached make a run skip pages whose entry is cached
	// and unexpired. Either nil means refresh everything.
	missingOnly func() bool
	isCached    func(path string) bool
	concurrency int // 0 means preloadConcurrency
	now         func() time.Time
	logger      *slog.Logger // nil discards

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	status  preloadStatus
	wg      sync.WaitGroup
}

func (p *pagePreloader) log() *slog.Logger {
	if p.logger != nil {
		return p.logger
	}
	return slog.New(slog.DiscardHandler)
}

// errRenderPanic marks an error made from a recovered panic, already logged.
var errRenderPanic = errors.New("panic")

// renderSafe runs one render; a panic becomes an error so one bad page cannot
// take the process down.
func (p *pagePreloader) renderSafe(ctx context.Context, path string) (cached bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			p.log().Error("preload render panicked", "path", path, "panic", r, "stack", string(debug.Stack()))
			cached, err = false, fmt.Errorf("%w: %v", errRenderPanic, r)
		}
	}()
	return p.render(ctx, path)
}

func (p *pagePreloader) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// Trigger starts a run in its own goroutine. It returns false when a run is
// already in progress or the cache is off. A nil parent runs on Background.
func (p *pagePreloader) Trigger(parent context.Context) bool {
	if p.enabled != nil && !p.enabled() {
		return false
	}
	if parent == nil {
		parent = context.Background()
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	p.running = true
	p.cancel = cancel
	p.status.Running = true
	p.status.Done, p.status.Total = 0, 0
	p.wg.Add(1)
	p.mu.Unlock()

	go p.run(ctx)
	return true
}

// Status returns a snapshot of the current or last run.
func (p *pagePreloader) Status() preloadStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Cancel stops a running preload; it is a no-op when idle.
func (p *pagePreloader) Cancel() {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Wait blocks until no run is in progress.
func (p *pagePreloader) Wait() { p.wg.Wait() }

func (p *pagePreloader) run(ctx context.Context) {
	defer p.wg.Done()
	start := p.clock()
	var startEvictions uint64
	if p.evictions != nil {
		startEvictions = p.evictions()
	}

	var (
		pages, errs int
		skipped     int
		failures    []preloadFailure
		failLogs    int
		sizeStop    bool
		paths       []string
	)
	list, err := p.paths(ctx)
	if err != nil {
		errs++
		failures = append(failures, preloadFailure{Path: "(page list)", Reason: failureReason(err)})
		p.log().Warn("preload url listing failed", "err", err)
	} else {
		paths = list
	}
	p.mu.Lock()
	p.status.Total = len(paths)
	p.mu.Unlock()

	work := make(chan string)
	var wg sync.WaitGroup
	n := p.concurrency
	if n <= 0 {
		n = preloadConcurrency
	}
	var resMu sync.Mutex
	onlyMissing := p.missingOnly != nil && p.isCached != nil && p.missingOnly()
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				resMu.Lock()
				stopped := sizeStop
				resMu.Unlock()
				if stopped {
					continue // keep draining so the feeder never blocks
				}
				if onlyMissing && p.isCached(path) {
					resMu.Lock()
					skipped++
					resMu.Unlock()
					p.mu.Lock()
					p.status.Done++
					p.mu.Unlock()
					continue
				}
				cached, err := p.renderSafe(ctx, path)
				resMu.Lock()
				if err != nil && !errors.Is(err, errRenderPanic) {
					if failLogs++; failLogs <= maxFailureLogs {
						p.log().Warn("preload render failed", "path", path, "err", err)
					}
				}
				switch {
				case err != nil:
					errs++
					if len(failures) < maxFailures {
						failures = append(failures, preloadFailure{Path: path, Reason: failureReason(err)})
					}
				case cached:
					pages++
				}
				if p.evictions != nil && p.evictions() > startEvictions {
					sizeStop = true
				}
				resMu.Unlock()
				p.mu.Lock()
				p.status.Done++
				p.mu.Unlock()
			}
		}()
	}
feed:
	for _, path := range paths {
		select {
		case <-ctx.Done():
			break feed
		case work <- path:
		}
		resMu.Lock()
		stop := sizeStop
		resMu.Unlock()
		if stop {
			break
		}
	}
	close(work)
	wg.Wait()
	if failLogs > maxFailureLogs {
		p.log().Warn(fmt.Sprintf("%d more preload failures suppressed", failLogs-maxFailureLogs))
	}

	reason := ""
	switch {
	case sizeStop:
		reason = preloadStopSizeLimit
	case ctx.Err() != nil:
		reason = preloadStopCancelled
	}
	end := p.clock()
	p.mu.Lock()
	p.cancel()
	p.running = false
	p.status = preloadStatus{
		Done: p.status.Done, Total: p.status.Total,
		HasResult: true, Finished: end, Pages: pages, Skipped: skipped, Errors: errs, Failures: failures,
		Duration: end.Sub(start), StoppedEarly: reason != "", Reason: reason,
	}
	p.mu.Unlock()
}

// preloadBase is the pinned public base URL; empty when PUBLIC_BASE_URL is unset.
func (s *Server) preloadBase() string {
	if s.cfg == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/")
}

// preloadPaths lists the URL paths of the served sitemap set, kept to the
// pinned base URL.
func (s *Server) preloadPaths(ctx context.Context) ([]string, error) {
	base := s.preloadBase()
	if base == "" {
		return nil, errors.New("PUBLIC_BASE_URL is not set")
	}
	in, err := s.loadSitemapInputFor(ctx, base)
	if err != nil {
		return nil, err
	}
	plan := planSitemap(in)
	var locs []string
	for _, g := range plan.groups {
		urls, ok := plan.GroupURLs(g.key)
		if !ok {
			continue
		}
		for _, u := range s.applySitemapFilter(ctx, g.key, urls) {
			locs = append(locs, u.Loc)
		}
	}
	return preloadPathsFromLocs(base, locs), nil
}

// preloadPathsFromLocs keeps the locs under base, drops query strings, and
// returns de-duplicated paths in order ("/" for the base itself).
func preloadPathsFromLocs(base string, locs []string) []string {
	bu, err := url.Parse(base)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, loc := range locs {
		if loc != base && !strings.HasPrefix(loc, base+"/") {
			continue
		}
		u, err := url.Parse(loc)
		if err != nil || u.Host != bu.Host || u.RawQuery != "" {
			continue
		}
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// preloadRender renders path through the public handlers (not the router, so
// no agent hit or 404 log rows are written) and overwrites its cache entry.
// cached is true for a 200 the shared cache may keep.
func (s *Server) preloadRender(ctx context.Context, path string) (bool, error) {
	if s.renderHandlers == nil {
		return false, errors.New("render handlers are not mounted")
	}
	base := s.preloadBase()
	if base == "" {
		return false, errors.New("PUBLIC_BASE_URL is not set")
	}
	bu, err := url.Parse(base)
	if err != nil {
		return false, err
	}
	hp := *s.renderHandlers // copy: the shared handlers keep their 404 logging
	hp.log404 = nil

	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(withRefresh(ctx))
	req.Host = bu.Host
	rec := httptest.NewRecorder()
	if path == "/" {
		hp.handleRenderHome(rec, req)
	} else {
		hp.handleRenderCatchAll(rec, req)
	}
	if rec.Code != http.StatusOK {
		if rec.Code >= 300 && rec.Code < 400 {
			return false, fmt.Errorf("redirect %d", rec.Code)
		}
		return false, errors.New(strconv.Itoa(rec.Code))
	}
	return strings.HasPrefix(rec.Header().Get("Cache-Control"), "public"), nil
}

// preloadCached reports whether path already has an unexpired rendered entry,
// under the same key the render path uses. path is the escaped form from the
// sitemap; serve keys on the decoded path without a trailing slash.
func (s *Server) preloadCached(path string) bool {
	if s.renderEngine == nil || s.infra == nil || s.infra.Cache == nil {
		return false
	}
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	body, err := s.infra.Cache.Get(context.Background(), cache.RenderedPageKey(s.renderEngine.Version(), path))
	return err == nil && len(body) > 0
}
