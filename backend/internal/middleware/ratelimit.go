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
// are swept here, at most once per sweepEvery, so an idle limiter runs no
// goroutine and no timer; the map cannot grow without requests.
func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	if now.Sub(rl.lastSweep) >= rl.sweepEvery {
		rl.lastSweep = now
		for k, v := range rl.visitors {
			if now.Sub(v.lastSeen) > rl.cleanup {
				delete(rl.visitors, k)
			}
		}
	}

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: now}
		return limiter
	}

	v.lastSeen = now
	return v.limiter
}

// Stop is kept for callers; the limiter has no background work to stop.
func (rl *RateLimiter) Stop() {}

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
