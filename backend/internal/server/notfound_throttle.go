package server

import (
	"sync"
	"time"
)

// nfThrottle counts not-found responses per client bucket. Each bucket holds
// perMin tokens and refills at perMin per minute; a response that finds it
// empty blocks the client for block. It never produces a response of its own:
// callers answer a blocked client with the standard 404, so a sweep learns
// nothing while blocked. perMin <= 0 disables it.
type nfThrottle struct {
	mu      sync.Mutex
	buckets map[string]*nfBucket
	perMin  int
	block   time.Duration
	now     func() time.Time
	swept   time.Time
	// maxBuckets caps the tracked clients; past it new keys are not tracked.
	maxBuckets int
	// blocks holds the end time of every running block, oldest first (every
	// block lasts the same time, so start order is end order). Its length is
	// the blocked-client count without scanning the map.
	blocks []time.Time
	// onBlock, when set, is called (outside the lock) each time a bucket
	// starts a block, with the number of buckets blocked at that moment.
	onBlock func(blocked int)
}

type nfBucket struct {
	tokens       float64
	last         time.Time
	blockedUntil time.Time
}

const nfSweepEvery = time.Minute

// nfMaxBuckets bounds the throttle's memory: a client with a routed IPv6
// prefix can present millions of /64 keys. Beyond it a new client is not
// tracked (the throttle fails open; the refusal itself never does).
const nfMaxBuckets = 100_000

func newNFThrottle(perMin int, block time.Duration) *nfThrottle {
	return &nfThrottle{buckets: map[string]*nfBucket{}, perMin: perMin, block: block, now: time.Now, maxBuckets: nfMaxBuckets}
}

func (t *nfThrottle) enabled() bool { return t != nil && t.perMin > 0 }

// charge records one not-found response for key and reports whether the key
// is now blocked.
func (t *nfThrottle) charge(key string) bool {
	if !t.enabled() {
		return false
	}
	t.mu.Lock()
	isBlocked, blockedBuckets := t.chargeLocked(key)
	t.mu.Unlock()
	if blockedBuckets > 0 && t.onBlock != nil {
		t.onBlock(blockedBuckets)
	}
	return isBlocked
}

// chargeLocked does the bucket arithmetic; the caller holds mu. It reports
// whether key is blocked and, when this charge started the block, how many
// buckets are blocked now (0 otherwise).
func (t *nfThrottle) chargeLocked(key string) (isBlocked bool, startedWith int) {
	now := t.now()
	t.sweep(now)
	b := t.buckets[key]
	if b == nil {
		if len(t.buckets) >= t.maxBuckets {
			return false, 0
		}
		b = &nfBucket{tokens: float64(t.perMin), last: now}
		t.buckets[key] = b
	}
	if now.Before(b.blockedUntil) {
		return true, 0
	}
	b.tokens += now.Sub(b.last).Seconds() * float64(t.perMin) / 60
	if b.tokens > float64(t.perMin) {
		b.tokens = float64(t.perMin)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return false, 0
	}
	b.blockedUntil = now.Add(t.block)
	b.tokens = float64(t.perMin) // a fresh bucket when the block ends
	for len(t.blocks) > 0 && !now.Before(t.blocks[0]) {
		t.blocks = t.blocks[1:]
	}
	t.blocks = append(t.blocks, b.blockedUntil)
	return true, len(t.blocks)
}

// blocked reports whether key is inside a block, without charging it.
func (t *nfThrottle) blocked(key string) bool {
	if !t.enabled() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buckets[key]
	return b != nil && t.now().Before(b.blockedUntil)
}

// sweep drops idle buckets (full, not blocked) at most once a minute, so the
// map stays small without a background goroutine. Caller holds mu.
func (t *nfThrottle) sweep(now time.Time) {
	if now.Sub(t.swept) < nfSweepEvery {
		return
	}
	t.swept = now
	for k, b := range t.buckets {
		if now.Before(b.blockedUntil) {
			continue
		}
		if b.tokens+now.Sub(b.last).Seconds()*float64(t.perMin)/60 >= float64(t.perMin) {
			delete(t.buckets, k)
		}
	}
}
