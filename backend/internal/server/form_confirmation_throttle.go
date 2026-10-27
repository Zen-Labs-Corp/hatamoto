package server

import (
	"strings"
	"sync"
	"time"
)

const (
	// confirmationCooldown is the minimum gap between two confirmation emails
	// to the same recipient for the same form.
	confirmationCooldown = 10 * time.Minute
	// confirmationMaxTracked bounds the cooldown map; when full of live
	// entries, further confirmations are skipped rather than growing memory.
	confirmationMaxTracked = 10000
	// confirmationMaxInFlight caps concurrent confirmation sends.
	confirmationMaxInFlight = 4
)

// confirmationThrottle limits form confirmation mail so the public submit
// endpoint cannot be used as a mail relay. State is in memory and therefore
// PER PROCESS: with N replicas a recipient can receive up to N emails per
// cooldown window (the per-IP route limiter is per process as well). The zero
// value is ready to use.
type confirmationThrottle struct {
	mu   sync.Mutex
	seen map[string]time.Time // form|recipient -> time of last send
	once sync.Once
	sem  chan struct{}
}

// allow records and returns true when no confirmation went to this recipient
// for this form within the cooldown. Returns false (skip) otherwise.
func (t *confirmationThrottle) allow(formID, to string, now time.Time) bool {
	key := formID + "|" + strings.ToLower(to)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = make(map[string]time.Time)
	}
	if last, ok := t.seen[key]; ok && now.Sub(last) < confirmationCooldown {
		return false
	}
	if len(t.seen) >= confirmationMaxTracked {
		for k, at := range t.seen { // TTL sweep
			if now.Sub(at) >= confirmationCooldown {
				delete(t.seen, k)
			}
		}
		if len(t.seen) >= confirmationMaxTracked {
			return false
		}
	}
	t.seen[key] = now
	return true
}

// tryAcquire reserves a send slot without blocking; false means the cap is reached.
func (t *confirmationThrottle) tryAcquire() bool {
	t.once.Do(func() { t.sem = make(chan struct{}, confirmationMaxInFlight) })
	select {
	case t.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (t *confirmationThrottle) release() { <-t.sem }
