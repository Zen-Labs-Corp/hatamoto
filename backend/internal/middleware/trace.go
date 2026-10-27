package middleware

import (
	"fmt"
	"net/http"
)

// TraceHook, when set, is called with a short tag each time a request enters
// one of the gates (auth, CSRF, role, rate limit; the server adds tenant and
// capture). Tests use it to pin which gates guard which route; production
// never sets it. It observes only: every gate still enforces.
var TraceHook func(r *http.Request, tag string)

func trace(r *http.Request, tag string) {
	if TraceHook != nil {
		TraceHook(r, tag)
	}
}

// Trace lets the server package tag its own middleware.
func Trace(r *http.Request, tag string) { trace(r, tag) }

func (rl *RateLimiter) traceTag() string { return fmt.Sprintf("rl(%v,%d)", float64(rl.rate), rl.burst) }
