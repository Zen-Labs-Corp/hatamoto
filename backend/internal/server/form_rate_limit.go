package server

import (
	"context"
	"sync"
	"time"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/middleware"
)

const (
	formRateWindow = time.Hour
	// formRateMaxTracked bounds the no-IP counter; when full of live entries,
	// new visitors are not counted (the limiter fails open): failing closed
	// would let a flood of distinct addresses mark everyone else's send as spam.
	formRateMaxTracked = 10000
)

// formRateReason is the spam reason of a send over a form's hourly limit.
const formRateReason = "rate limit"

// ipFreeLimiter counts sends per form and visitor in memory, for forms that
// store no address. State is PER PROCESS and lost on restart. The zero value
// is ready to use.
type ipFreeLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time // form|bucket -> send times inside the window
}

// over records a send and reports whether the visitor already made limit sends
// for this form within the window.
func (l *ipFreeLimiter) over(formID, bucket string, limit int, now time.Time) bool {
	key := formID + "|" + bucket
	cutoff := now.Add(-formRateWindow)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = make(map[string][]time.Time)
	}
	recent := l.hits[key][:0]
	for _, at := range l.hits[key] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	if len(recent) == 0 && len(l.hits) >= formRateMaxTracked {
		for k, times := range l.hits { // sweep entries that aged out
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
		if len(l.hits) >= formRateMaxTracked {
			return false
		}
	}
	if len(recent) >= limit {
		l.hits[key] = recent
		return true
	}
	l.hits[key] = append(recent, now)
	return false
}

// formOverRateLimit reports whether this send exceeds the form's per-visitor
// hourly limit. With the address stored, the limit counts the form's recent
// submissions from it; with no-IP on or storage off, no address is stored, so
// a per-process counter keyed on the address bucket is used instead. A failed count lets the
// send through.
func (s *Server) formOverRateLimit(ctx context.Context, f *form.Form, ip string, now time.Time) bool {
	limit := f.Settings.RateLimitPerHour
	if limit <= 0 || ip == "" {
		return false
	}
	if f.Settings.NoIP || f.Settings.StoreOff {
		return s.formRates.over(f.ID.String(), middleware.IPBucket(ip), limit, now)
	}
	n, err := s.formService.CountRecentSubmissionsByIP(ctx, f.ID, ip, now.Add(-formRateWindow))
	if err != nil {
		s.logger.Error("form rate limit count failed", "error", err, "form_id", f.ID.String())
		return false
	}
	return n >= limit
}
