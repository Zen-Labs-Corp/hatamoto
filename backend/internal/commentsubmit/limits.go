package commentsubmit

import (
	"sync"
	"time"

	"hatamoto.dev/core/internal/middleware"
)

const (
	defaultSurgeLimit = 30
	surgeWindow       = 10 * time.Minute
	slowModeCap       = 10000
)

// ReasonSlowMode is the hold reason this part adds next to the model's comment.Reason* constants.
const ReasonSlowMode = "slow mode"

// Limits holds the in-memory submit limits. Keys are hashes or ids, never a raw
// IP or email. State is per process.
type Limits struct {
	ip, net, email, entity *middleware.RateLimiter
	slow                   *slowMode
	surge                  *Surge
}

// NewLimits builds the limits; now is the clock for slow mode and the surge window.
func NewLimits(now func() time.Time) *Limits {
	if now == nil {
		now = time.Now
	}
	return &Limits{
		ip:     middleware.NewRateLimiter(1.0/20, 3),
		net:    middleware.NewRateLimiter(1.0/6, 10),
		email:  middleware.NewRateLimiter(1.0/30, 3),
		entity: middleware.NewRateLimiter(1.0/2, 20),
		slow:   &slowMode{now: now, last: map[string]time.Time{}},
		surge:  &Surge{now: now, limit: defaultSurgeLimit},
	}
}

// Allow reports whether a submit passes every bucket; false means drop it
// (answer with a fake success). Every bucket is charged, as a bucket that never
// sees a request would let that axis go unlimited.
func (l *Limits) Allow(ipHash, netHash, emailHash, entityID string) bool {
	a := l.ip.Allow(ipHash)
	b := l.net.Allow(netHash)
	c := l.email.Allow(emailHash)
	d := l.entity.Allow(entityID)
	return a && b && c && d
}

// InSlowMode reports whether the commenter posted on the entity within
// interval. The caller exempts trusted commenters; interval 0 means off.
func (l *Limits) InSlowMode(commenterKey, entityID string, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	return l.slow.within(commenterKey+"|"+entityID, interval)
}

// MarkPosted records a post by the commenter on the entity.
func (l *Limits) MarkPosted(commenterKey, entityID string) {
	l.slow.mark(commenterKey + "|" + entityID)
}

// Surge returns the site-wide surge window.
func (l *Limits) Surge() *Surge { return l.surge }

// Stop ends the limiters' background cleanup.
func (l *Limits) Stop() {
	l.ip.Stop()
	l.net.Stop()
	l.email.Stop()
	l.entity.Stop()
}

type slowMode struct {
	mu   sync.Mutex
	now  func() time.Time
	last map[string]time.Time
}

func (s *slowMode) within(key string, interval time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.last[key]
	return ok && s.now().Sub(t) < interval
}

func (s *slowMode) mark(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.last[key]; !ok && len(s.last) >= slowModeCap {
		var oldestKey string
		var oldest time.Time
		first := true
		for k, t := range s.last {
			if first || t.Before(oldest) {
				oldestKey, oldest, first = k, t, false
			}
		}
		delete(s.last, oldestKey)
	}
	s.last[key] = s.now()
}

// Surge is a site-wide sliding window of submits. It is checked before any
// skippable DB write and does no I/O.
type Surge struct {
	mu     sync.Mutex
	now    func() time.Time
	limit  int
	times  []time.Time // within the window, oldest first, at most limit+1
	active bool
}

// Note records a submit. active is true while more than the limit arrived
// within the window; started is true exactly once per episode, so the caller
// logs a warning once.
func (s *Surge) Note() (active bool, started bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	i := 0
	for i < len(s.times) && now.Sub(s.times[i]) >= surgeWindow {
		i++
	}
	s.times = append(s.times[i:], now)
	if len(s.times) > s.limit+1 {
		s.times = s.times[len(s.times)-(s.limit+1):]
	}
	active = len(s.times) > s.limit
	started = active && !s.active
	s.active = active
	return active, started
}
