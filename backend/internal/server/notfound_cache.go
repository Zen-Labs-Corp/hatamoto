package server

import (
	"container/list"
	"sync"
	"time"
)

// Bounds of the rendered-404 store. A flood of random paths only churns it.
const (
	maxNotFoundEntries = 256
	maxNotFoundBytes   = 4 << 20
	maxNotFoundBody    = 256 << 10 // a larger body is not stored
	maxNotFoundPath    = 256       // a longer path is never keyed
)

// notFoundCache keeps rendered 404 bodies per (theme version, path). An entry
// is valid only for the page-cache generation it was rendered under (any
// page-cache drop makes it stale) and until it expires. Bodies are shared
// read-only. A nil cache is off.
type notFoundCache struct {
	mu       sync.Mutex
	ll       *list.List // front = most recently used
	m        map[string]*list.Element
	bytes    int64
	maxItems int
	maxBytes int64
}

type notFoundEntry struct {
	key     string
	body    []byte
	gen     uint64
	expires time.Time
}

func newNotFoundCache() *notFoundCache {
	return &notFoundCache{
		ll:       list.New(),
		m:        map[string]*list.Element{},
		maxItems: maxNotFoundEntries,
		maxBytes: maxNotFoundBytes,
	}
}

func entrySize(e *notFoundEntry) int64 { return int64(len(e.key) + len(e.body)) }

// get returns the body stored for key when it was rendered under gen and has
// not expired; a stale entry is dropped.
func (c *notFoundCache) get(key string, gen uint64, now time.Time) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*notFoundEntry)
	if e.gen != gen || !now.Before(e.expires) {
		c.remove(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.body, true
}

// put stores body for key under gen until expires, evicting least recently
// used entries past either cap. A body over maxNotFoundBody is ignored.
func (c *notFoundCache) put(key string, body []byte, gen uint64, expires time.Time) {
	if c == nil || len(body) > maxNotFoundBody {
		return
	}
	e := &notFoundEntry{key: key, body: body, gen: gen, expires: expires}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.m[key]; ok {
		c.remove(old)
	}
	if entrySize(e) > c.maxBytes {
		return
	}
	c.m[key] = c.ll.PushFront(e)
	c.bytes += entrySize(e)
	for c.ll.Len() > c.maxItems || c.bytes > c.maxBytes {
		c.remove(c.ll.Back())
	}
}

func (c *notFoundCache) remove(el *list.Element) {
	e := c.ll.Remove(el).(*notFoundEntry)
	delete(c.m, e.key)
	c.bytes -= entrySize(e)
}
