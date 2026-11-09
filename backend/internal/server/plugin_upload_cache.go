package server

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// A plugin reads an upload in chunks, one host call each. When the staged
// file lives in object storage, opening it means downloading it, so the opened
// file is kept for a while and shared by the calls that follow.
const (
	uploadCacheTTL = 2 * time.Minute
	uploadCacheMax = 16
)

// uploadCache keeps the opened (already unlinked) spool files of staged
// uploads, keyed by upload id and blob key. Idle files are closed by a timer
// once they pass the TTL, as are the oldest ones beyond the limit. The download
// of a file runs outside the lock, once per key: other callers wait for it
// without blocking the rest of the cache.
type uploadCache struct {
	mu    sync.Mutex
	m     map[string]*cachedUpload
	open  func(ctx context.Context, key string) (*os.File, int64, error)
	ttl   time.Duration
	max   int
	now   func() time.Time
	timer *time.Timer // pending idle sweep; nil when none is scheduled
}

type cachedUpload struct {
	ready   chan struct{} // closed when the download ended; f, size and err are set then
	f       *os.File
	size    int64
	err     error
	last    time.Time
	refs    int  // readers (and the download) that have not let go yet
	evicted bool // out of the map; the file closes with the last reader
}

func newUploadCache(open func(ctx context.Context, key string) (*os.File, int64, error)) *uploadCache {
	return &uploadCache{m: map[string]*cachedUpload{}, open: open, ttl: uploadCacheTTL, max: uploadCacheMax, now: time.Now}
}

// get returns a reader over the staged file of upload id, opening it once.
// Closing the reader only releases it: the cache owns the file. A caller whose
// ctx ends stops waiting at once.
func (c *uploadCache) get(ctx context.Context, id, blobKey string) (io.ReaderAt, int64, error) {
	k := id + "\x00" + blobKey
	for {
		c.mu.Lock()
		now := c.now()
		c.evict(now)
		e := c.m[k]
		creator := e == nil
		if creator {
			e = &cachedUpload{ready: make(chan struct{})}
			c.m[k] = e
			c.armLocked()
		}
		e.last = now
		e.refs++
		if creator {
			c.evict(now) // the new entry is the newest: only older ones go
		}
		c.mu.Unlock()

		if creator {
			f, size, err := c.open(ctx, blobKey)
			c.mu.Lock()
			e.f, e.size, e.err = f, size, err
			if err != nil && c.m[k] == e {
				delete(c.m, k)
				e.evicted = true
			}
			c.mu.Unlock()
			close(e.ready)
		} else {
			select {
			case <-e.ready:
			case <-ctx.Done():
				c.release(e)
				return nil, 0, ctx.Err()
			}
		}
		if e.err != nil {
			c.release(e)
			// The download of another caller that was cancelled is not this
			// caller's failure: try again with our own context.
			if !creator && ctx.Err() == nil && (errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded)) {
				continue
			}
			return nil, 0, e.err
		}
		return &cachedReader{c: c, e: e}, e.size, nil
	}
}

// release gives up one reference of e and closes its file when it was the last
// one of an entry the cache no longer holds.
func (c *uploadCache) release(e *cachedUpload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.refs--
	if e.evicted && e.refs == 0 && e.f != nil {
		e.f.Close()
	}
}

// armLocked schedules an idle sweep when the cache holds entries and none is
// pending, so files are released without another call arriving. The caller
// holds c.mu.
func (c *uploadCache) armLocked() {
	if c.timer == nil && len(c.m) > 0 {
		c.timer = time.AfterFunc(c.ttl, c.sweep)
	}
}

func (c *uploadCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timer = nil
	c.evict(c.now())
	c.armLocked()
}

// evict drops entries idle for longer than the TTL, then the oldest beyond the
// limit. The caller holds c.mu.
func (c *uploadCache) evict(now time.Time) {
	for k, e := range c.m {
		if now.Sub(e.last) > c.ttl && e.refs == 0 {
			c.drop(k, e)
		}
	}
	if len(c.m) <= c.max {
		return
	}
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return c.m[keys[i]].last.Before(c.m[keys[j]].last) })
	for _, k := range keys[:len(keys)-c.max] {
		c.drop(k, c.m[k])
	}
}

func (c *uploadCache) drop(k string, e *cachedUpload) {
	delete(c.m, k)
	e.evicted = true
	if e.refs == 0 && e.f != nil {
		e.f.Close()
	}
}

// closeAll closes every cached file and stops the idle sweep.
func (c *uploadCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	for k, e := range c.m {
		c.drop(k, e)
	}
}

type cachedReader struct {
	c    *uploadCache
	e    *cachedUpload
	once sync.Once
}

func (r *cachedReader) ReadAt(p []byte, off int64) (int, error) { return r.e.f.ReadAt(p, off) }

// Close releases this reader; the file stays open for the next call unless the
// cache already dropped it.
func (r *cachedReader) Close() error {
	r.once.Do(func() { r.c.release(r.e) })
	return nil
}
