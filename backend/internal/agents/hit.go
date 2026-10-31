package agents

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Capture sources, as stored in hatamoto_agent_hits.source.
const (
	SourceMiddleware = "middleware" // Next.js middleware / robots.txt route ping
	SourceBackend    = "backend"    // captured by the Go API itself
	SourceLog        = "log"        // access-log ingest
)

// Verification states, as stored in hatamoto_agent_hits.verification.
const (
	VerificationPending      = "pending"
	VerificationVerified     = "verified"
	VerificationSpoofed      = "spoofed"
	VerificationUnverifiable = "unverifiable"
)

// RawHit is one observed request, before classification. Status 0 means
// "unknown": the writer derives it (resolver for middleware hits, 200 for
// backend hits).
type RawHit struct {
	At     time.Time
	UA, IP string
	Method string
	Path   string
	Status int
	// RedirectHint is a 3xx fallback used only when the resolver returns 404.
	RedirectHint int
	Bytes        int
	DurationMS   int
	Source       string
}

// Hit is a classified hit ready for storage.
type Hit struct {
	At           time.Time
	AgentID      string
	Operator     string
	Purpose      Purpose
	Verification string
	IP           *netip.Addr
	Method       string
	Path         string
	EntityID     *uuid.UUID
	Status       int // 0 = unknown
	Bytes        int64
	DurationMS   int
	Source       string
}

// ResolveFunc maps a public path to the entity it serves and the HTTP status a
// visitor would get (301/302 redirect, 200 served, 404 miss). It runs in the
// writer goroutine, never on the request path.
type ResolveFunc func(ctx context.Context, path string) (entityID *uuid.UUID, status int)

type resolved struct {
	entityID *uuid.UUID
	status   int
	expires  time.Time
}

// nilTTL bounds how long a result without an entity is remembered. A page
// published a moment after a crawler asked for it must not stay unattributed
// for the whole ttl.
const nilTTL = 5 * time.Second

// NewCachedResolver memoises fn per path for ttl (results without an entity
// for at most nilTTL), holding at most max entries (the cache is cleared when
// full: simple and bounded).
func NewCachedResolver(fn ResolveFunc, ttl time.Duration, max int) ResolveFunc {
	return newCachedResolver(fn, ttl, max, time.Now)
}

func newCachedResolver(fn ResolveFunc, ttl time.Duration, max int, now func() time.Time) ResolveFunc {
	var mu sync.Mutex
	cache := make(map[string]resolved)
	return func(ctx context.Context, path string) (*uuid.UUID, int) {
		mu.Lock()
		if r, ok := cache[path]; ok && now().Before(r.expires) {
			mu.Unlock()
			return r.entityID, r.status
		}
		mu.Unlock()

		id, status := fn(ctx, path)

		mu.Lock()
		if len(cache) >= max {
			clear(cache)
		}
		keep := ttl
		if id == nil && nilTTL < keep {
			keep = nilTTL
		}
		cache[path] = resolved{entityID: id, status: status, expires: now().Add(keep)}
		mu.Unlock()
		return id, status
	}
}

const (
	maxPathLen = 512
	maxMethod  = 16
)

// cleanText makes s safe for a Postgres text column: invalid UTF-8 and NUL
// bytes are removed and the result is cut to at most max bytes on a rune
// boundary.
func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > max {
		s = s[:max]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// cleanPath strips the query/fragment and sanitises the rest.
func cleanPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return cleanText(p, maxPathLen)
}

// parseIP returns the unmapped address, or nil for empty/invalid input.
func parseIP(s string) *netip.Addr {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	a = a.Unmap().WithZone("")
	return &a
}
