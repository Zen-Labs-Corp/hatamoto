package middleware

import (
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter implements per-IP rate limiting.
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
	cleanup  time.Duration // a visitor unseen this long is dropped
	slash64  bool

	sweepEvery time.Duration // at most one sweep per this interval
	lastSweep  time.Time
	now        func() time.Time // time.Now; tests inject a clock
	timer      sweepTimer       // sweeps after a burst, once requests stop
}

// clock is now(), or time.Now for a zero-value limiter.
func (rl *RateLimiter) clock() time.Time {
	if rl.now == nil {
		return time.Now()
	}
	return rl.now()
}

// sweepLocked drops visitors unseen for cleanup. Caller holds mu.
func (rl *RateLimiter) sweepLocked(now time.Time) {
	rl.lastSweep = now
	for k, v := range rl.visitors {
		if now.Sub(v.lastSeen) > rl.cleanup {
			delete(rl.visitors, k)
		}
	}
}

// armLocked arms the cleanup timer while visitors remain. Caller holds mu.
func (rl *RateLimiter) armLocked() {
	if len(rl.visitors) > 0 {
		rl.timer.arm(rl.cleanup+rl.sweepEvery, rl.timerSweep)
	}
}

// timerSweep frees stale visitors when no request came to do it, and re-arms
// only if some remain.
func (rl *RateLimiter) timerSweep() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.timer.armed = false
	rl.sweepLocked(rl.clock())
	rl.armLocked()
}

// sweepTimer is a one-shot cleanup timer for a limiter's map. The owner arms
// it while the map is non-empty and holds its own lock around every call, so
// an empty (or never used) limiter has no timer at all.
type sweepTimer struct {
	t       *time.Timer
	armed   bool
	stopped bool
}

// arm schedules f after d unless a run is already pending or the limiter is stopped.
func (s *sweepTimer) arm(d time.Duration, f func()) {
	if s.armed || s.stopped {
		return
	}
	s.armed = true
	s.t = time.AfterFunc(d, f)
}

// stop cancels a pending run and refuses new ones.
func (s *sweepTimer) stop() {
	s.stopped, s.armed = true, false
	if s.t != nil {
		s.t.Stop()
	}
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter.
// rate: requests per second
// burst: maximum burst size
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate.Limit(rps),
		burst:    burst,
		cleanup:  3 * time.Minute,

		sweepEvery: time.Minute,
		lastSweep:  time.Now(),
		now:        time.Now,
	}
	return rl
}

// IPBucket is the key a client address is limited under: the address itself,
// or for IPv6 its /64, so one host with a whole prefix gets one bucket, not 2^64.
func IPBucket(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Is4() || a.Is4In6() {
		return ip
	}
	p, _ := a.Prefix(64)
	return p.String()
}

// ByIPv6Prefix makes the limiter count an IPv6 client by its /64 instead of its single
// address (see IPBucket) and returns it. Used where a host with a whole prefix must not
// get a bucket per address, such as the public demo.
func (rl *RateLimiter) ByIPv6Prefix() *RateLimiter {
	rl.slash64 = true
	return rl
}

// getVisitor retrieves or creates a rate limiter for an IP. Stale visitors
// are swept here, at most once per sweepEvery; a one-shot timer, armed only
// while the map is non-empty, frees them when requests stop.
func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.clock()
	if now.Sub(rl.lastSweep) >= rl.sweepEvery {
		rl.sweepLocked(now)
	}

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: now}
		rl.armLocked()
		return limiter
	}

	v.lastSeen = now
	return v.limiter
}

// Stop cancels the pending cleanup timer, if any.
func (rl *RateLimiter) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.timer.stop()
}

// Allow reports whether key may make a request now; it is the limiter's own
// bucket logic for callers that choose the key themselves (a session, say).
func (rl *RateLimiter) Allow(key string) bool {
	return rl.getVisitor(key).Allow()
}

// Middleware returns a rate limiting middleware.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace(r, rl.traceTag())
		ip := GetClientIP(r)
		if rl.slash64 {
			ip = IPBucket(ip)
		}
		limiter := rl.getVisitor(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AuthRateLimiter is a stricter rate limiter for auth endpoints.
func AuthRateLimiter() *RateLimiter {
	// 5 requests per second, burst of 10
	return NewRateLimiter(5, 10)
}

// APIRateLimiter is a general rate limiter for API endpoints.
func APIRateLimiter() *RateLimiter {
	// 100 requests per second, burst of 200
	return NewRateLimiter(100, 200)
}
