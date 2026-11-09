package server

import (
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
// uploads, keyed by upload id and blob key. Idle files are closed on the next
// call, as are the oldest ones beyond the limit.
type uploadCache struct {
	mu   sync.Mutex
	m    map[string]*cachedUpload
	open func(key string) (*os.File, int64, error)
	ttl  time.Duration
	max  int
	now  func() time.Time
}

type cachedUpload struct {
	f       *os.File
	size    int64
	last    time.Time
	refs    int  // readers that have not closed their handle yet
	evicted bool // out of the map; the file closes with the last reader
}

func newUploadCache(open func(key string) (*os.File, int64, error)) *uploadCache {
	return &uploadCache{m: map[string]*cachedUpload{}, open: open, ttl: uploadCacheTTL, max: uploadCacheMax, now: time.Now}
}

// get returns a reader over the staged file of upload id, opening it once.
// Closing the reader only releases it: the cache owns the file.
func (c *uploadCache) get(id, blobKey string) (io.ReaderAt, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.evict(now)
	k := id + "\x00" + blobKey
	e := c.m[k]
	if e == nil {
		f, size, err := c.open(blobKey)
		if err != nil {
			return nil, 0, err
		}
		e = &cachedUpload{f: f, size: size, last: now}
		c.m[k] = e
		c.evict(now) // the new entry is the newest: only older ones go
	}
	e.last = now
	e.refs++
	return &cachedReader{c: c, e: e}, e.size, nil
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
	if e.refs == 0 {
		e.f.Close()
	}
}

// closeAll closes every cached file.
func (c *uploadCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	r.once.Do(func() {
		r.c.mu.Lock()
		defer r.c.mu.Unlock()
		r.e.refs--
		if r.e.evicted && r.e.refs == 0 {
			r.e.f.Close()
		}
	})
	return nil
}
