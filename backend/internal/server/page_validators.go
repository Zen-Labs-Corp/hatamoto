package server

import (
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// maxPageValidators bounds the validator map; past it the least recently
// stamped key is evicted.
const maxPageValidators = 10_000

// pageValidators remembers, per rendered-page cache key, the body fingerprint
// and the time those bytes were first served. It lives outside the TTL cache
// so a purge or expiry followed by a byte-identical re-render keeps
// Last-Modified. In memory only: a restart starts over. Past the cap the least
// recently stamped key is dropped; floor keeps a dropped key from repeating a
// second already given out for other bytes.
// A nil *pageValidators means validators are off.
type pageValidators struct {
	mu    sync.Mutex
	m     map[string]*pageEntry
	root  pageEntry // list sentinel: root.next is the most recent, root.prev the least
	now   func() time.Time
	floor time.Time // no new entry gets a Last-Modified before this
	max   int
}

// pageEntry is one key's validator state and its place in the recency list.
type pageEntry struct {
	key        string
	fp         uint64
	since      time.Time
	prev, next *pageEntry
}

func newPageValidators(now func() time.Time) *pageValidators {
	if now == nil {
		now = time.Now
	}
	v := &pageValidators{
		m:     make(map[string]*pageEntry),
		now:   now,
		floor: now().UTC().Truncate(time.Second).Add(time.Second),
		max:   maxPageValidators,
	}
	v.root.next, v.root.prev = &v.root, &v.root
	return v
}

// clock returns the validators' current time.
func (v *pageValidators) clock() time.Time { return v.now() }

func (v *pageValidators) unlink(e *pageEntry) {
	e.prev.next, e.next.prev = e.next, e.prev
	e.prev, e.next = nil, nil
}

func (v *pageValidators) pushFront(e *pageEntry) {
	e.prev, e.next = &v.root, v.root.next
	v.root.next.prev = e
	v.root.next = e
}

// stamp records fp for key and returns its Last-Modified (UTC, whole seconds).
// The same fp keeps its time; a new fp gets a strictly later second than the
// previous bytes of that key, even within the same second. now is the request's
// single clock read (the caller's clock()); stamp never reads the clock itself,
// so one request sees one time.
func (v *pageValidators) stamp(key string, fp uint64, now time.Time) time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.m[key]
	if ok && e.fp == fp {
		if v.root.next != e {
			v.unlink(e)
			v.pushFront(e)
		}
		return e.since
	}
	now = now.UTC().Truncate(time.Second)
	since := now
	if ok && e.since.Add(time.Second).After(since) {
		since = e.since.Add(time.Second)
	}
	if v.floor.After(since) {
		since = v.floor
	}
	switch {
	case ok:
		v.unlink(e)
	case len(v.m) >= v.max:
		// Evict the least recent key and reuse its node. Raise floor past its
		// last second so, if it comes back with other bytes, it never repeats one.
		e = v.root.prev
		v.unlink(e)
		delete(v.m, e.key)
		if f := e.since.Add(time.Second); f.After(v.floor) {
			v.floor = f
		}
		e.key = key
		v.m[key] = e
	default:
		e = &pageEntry{key: key}
		v.m[key] = e
	}
	e.fp, e.since = fp, since
	v.pushFront(e)
	return since
}

// pageETag is the weak validator of a page body: W/"<16 hex>". It is weak
// because the same page is served under several content codings.
func pageETag(fp uint64) string {
	var b [20]byte
	copy(b[:], `W/"`)
	var raw [8]byte
	for i := range raw {
		raw[i] = byte(fp >> (56 - 8*i))
	}
	hex.Encode(b[3:19], raw[:])
	b[19] = '"'
	return string(b[:])
}

// bareETag is the opaque tag without the W/ prefix, the form ifNoneMatchHits
// compares against.
func bareETag(fp uint64) string { return pageETag(fp)[2:] }

// notModified applies RFC 9110 13.2.2: If-None-Match when present, otherwise
// If-Modified-Since for GET and HEAD while since is not in the future.
func notModified(r *http.Request, fp uint64, since, now time.Time) bool {
	if r.Header.Get("If-None-Match") != "" {
		return ifNoneMatchHits(r, bareETag(fp))
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if since.After(now) {
		return false
	}
	ims, err := http.ParseTime(r.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}
	return !since.After(ims)
}

// setValidators writes the ETag and, once since is not in the future,
// Last-Modified.
func setValidators(hd http.Header, fp uint64, since, now time.Time) {
	hd.Set("ETag", pageETag(fp))
	if !since.After(now) {
		hd.Set("Last-Modified", since.Format(http.TimeFormat))
	}
}
