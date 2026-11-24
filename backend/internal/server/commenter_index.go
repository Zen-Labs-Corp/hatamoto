package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

// commenterIndex resolves a plugin's commenter_key back to the commenter id
// without storing keys: per plugin it derives the key of every commenter once
// (pluginkey, one-way HMAC) and keeps key -> id in memory, then derives only
// recent commenters on a miss, and rescans all of them once a minute at most. It lives as long as the process;
// a new server secret means a new process and a new index.
type commenterIndex struct {
	list   func(ctx context.Context, since time.Time) ([]comment.CommenterStamp, error)
	derive func(plugin string, id uuid.UUID) string // pluginkey.Deriver.Commenter
	now    func() time.Time

	mu      sync.Mutex // guards plugins only
	plugins map[string]*pluginKeys
}

type pluginKeys struct {
	sem       chan struct{} // held (cap 1) during a scan: one at a time, waiters honour ctx
	ids       map[string]uuid.UUID
	scanned   bool      // a full scan succeeded
	watermark time.Time // newest created_at seen
	lastScan  time.Time
	lastFull  time.Time // when the last full scan succeeded
}

const (
	// keyScanOverlap re-reads commenters this far before the watermark:
	// created_at is set by the writer's clock, so a row can commit after a
	// newer one was scanned. Re-deriving is idempotent.
	keyScanOverlap = 10 * time.Minute
	// keyFullRescanGap: created_at is not monotonic (the importer writes the
	// date of a commenter's oldest imported comment), so the delta scan can
	// miss a row. A miss that finds the last full scan older than this
	// rescans everything.
	keyFullRescanGap = time.Minute
	// keyMissGap: a key that is not known triggers at most one delta scan per
	// plugin in this interval, so random keys cannot force repeated scans.
	keyMissGap = 2 * time.Second
	// keyScanTimeout bounds a scan that runs detached from the caller's deadline.
	keyScanTimeout = 30 * time.Second
)

// keyLen is the length of a commenter key (base64url, no padding, of a SHA-256 HMAC).
var keyLen = base64.RawURLEncoding.EncodedLen(sha256.Size)

// newCommenterIndex returns nil when there is nothing to list or derive with.
func newCommenterIndex(list func(context.Context, time.Time) ([]comment.CommenterStamp, error),
	derive func(string, uuid.UUID) string) *commenterIndex {
	if list == nil || derive == nil {
		return nil
	}
	return &commenterIndex{list: list, derive: derive, now: time.Now, plugins: map[string]*pluginKeys{}}
}

func (x *commenterIndex) forPlugin(plugin string) *pluginKeys {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := x.plugins[plugin]
	if p == nil {
		p = &pluginKeys{sem: make(chan struct{}, 1), ids: map[string]uuid.UUID{}}
		x.plugins[plugin] = p
	}
	return p
}

// resolve returns the commenter of plugin's key. ok=false for a nil index, an
// empty plugin, a key of the wrong length, or a key no commenter has. A scan
// runs under its own timeout, not the caller's deadline, so one that starts
// also finishes; a caller whose ctx ends first returns ctx's error and the
// scan completes for the next one. A failed scan is retried no sooner than
// keyMissGap later (until then the key counts as unknown).
func (x *commenterIndex) resolve(ctx context.Context, plugin, key string) (uuid.UUID, bool, error) {
	if x == nil || plugin == "" || len(key) != keyLen {
		return uuid.Nil, false, nil
	}
	p := x.forPlugin(plugin)
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return uuid.Nil, false, ctx.Err()
	}
	if id, ok := p.ids[key]; ok {
		<-p.sem
		return id, true, nil
	}
	if !p.lastScan.IsZero() && x.now().Sub(p.lastScan) < keyMissGap {
		<-p.sem
		return uuid.Nil, false, nil
	}
	type result struct {
		id  uuid.UUID
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-p.sem }()
		err := x.scan(context.WithoutCancel(ctx), plugin, p)
		id, ok := p.ids[key] // still holding the slot: no other scan writes
		done <- result{id, ok, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return uuid.Nil, false, r.err
		}
		return r.id, r.ok, nil
	case <-ctx.Done():
		return uuid.Nil, false, ctx.Err()
	}
}

// scan lists and derives under the plugin's sem.
func (x *commenterIndex) scan(ctx context.Context, plugin string, p *pluginKeys) error {
	ctx, cancel := context.WithTimeout(ctx, keyScanTimeout)
	defer cancel()
	var since time.Time
	full := !p.scanned || x.now().Sub(p.lastFull) >= keyFullRescanGap
	if !full {
		since = p.watermark.Add(-keyScanOverlap)
	}
	rows, err := x.list(ctx, since)
	if err != nil {
		p.lastScan = x.now()
		return err
	}
	for _, r := range rows {
		if k := x.derive(plugin, r.ID); k != "" {
			p.ids[k] = r.ID
		}
		if r.CreatedAt.After(p.watermark) {
			p.watermark = r.CreatedAt
		}
	}
	p.scanned = true
	p.lastScan = x.now()
	if full {
		p.lastFull = p.lastScan
	}
	return nil
}
