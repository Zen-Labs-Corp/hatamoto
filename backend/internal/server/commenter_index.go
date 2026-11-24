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
// commenters created since the last scan. It lives as long as the process;
// a new server secret means a new process and a new index.
type commenterIndex struct {
	list   func(ctx context.Context, since time.Time) ([]comment.CommenterStamp, error)
	derive func(plugin string, id uuid.UUID) string // pluginkey.Deriver.Commenter
	now    func() time.Time

	mu      sync.Mutex // guards plugins only
	plugins map[string]*pluginKeys
}

type pluginKeys struct {
	mu        sync.Mutex // held during a scan: one scan per plugin at a time
	ids       map[string]uuid.UUID
	scanned   bool      // a full scan succeeded
	watermark time.Time // newest created_at seen
	lastScan  time.Time
}

const (
	// keyScanOverlap re-reads commenters this far before the watermark:
	// created_at is the inserting transaction's start, so a row can commit
	// after a newer one was scanned. Re-deriving is idempotent.
	keyScanOverlap = 10 * time.Minute
	// keyMissGap: a key that is not known triggers at most one delta scan per
	// plugin in this interval, so random keys cannot force repeated scans.
	keyMissGap = 2 * time.Second
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
		p = &pluginKeys{ids: map[string]uuid.UUID{}}
		x.plugins[plugin] = p
	}
	return p
}

// resolve returns the commenter of plugin's key. ok=false for a nil index, an
// empty plugin, a key of the wrong length, or a key no commenter has. A list
// error is returned and leaves the state unchanged, so the next call retries.
func (x *commenterIndex) resolve(ctx context.Context, plugin, key string) (uuid.UUID, bool, error) {
	if x == nil || plugin == "" || len(key) != keyLen {
		return uuid.Nil, false, nil
	}
	p := x.forPlugin(plugin)
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.ids[key]; ok {
		return id, true, nil
	}
	if p.scanned && x.now().Sub(p.lastScan) < keyMissGap {
		return uuid.Nil, false, nil
	}
	var since time.Time
	if p.scanned {
		since = p.watermark.Add(-keyScanOverlap)
	}
	rows, err := x.list(ctx, since)
	if err != nil {
		return uuid.Nil, false, err
	}
	for _, r := range rows {
		if k := x.derive(plugin, r.ID); k != "" {
			p.ids[k] = r.ID
		}
		if r.CreatedAt.After(p.watermark) {
			p.watermark = r.CreatedAt
		}
	}
	p.scanned, p.lastScan = true, x.now()
	id, ok := p.ids[key]
	return id, ok, nil
}
