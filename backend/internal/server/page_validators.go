package server

import (
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// maxPageValidators bounds the validator map; past it the map starts over.
const maxPageValidators = 10_000

// pageValidators remembers, per rendered-page cache key, the body fingerprint
// and the time those bytes were first served. It lives outside the TTL cache
// so a purge or expiry followed by a byte-identical re-render keeps
// Last-Modified. In memory only: a restart starts over, and floor keeps
// Last-Modified from ever repeating a second already given out for other bytes.
// A nil *pageValidators means validators are off.
type pageValidators struct {
	mu    sync.Mutex
	m     map[string]pageStamp
	now   func() time.Time
	floor time.Time // no new entry gets a Last-Modified before this
	max   int
}

type pageStamp struct {
	fp    uint64
	since time.Time
}

func newPageValidators(now func() time.Time) *pageValidators {
	if now == nil {
		now = time.Now
	}
	return &pageValidators{
		m:     make(map[string]pageStamp),
		now:   now,
		floor: now().UTC().Truncate(time.Second).Add(time.Second),
		max:   maxPageValidators,
	}
}

// clock returns the validators' current time.
func (v *pageValidators) clock() time.Time { return v.now() }

// stamp records fp for key and returns its Last-Modified (UTC, whole seconds).
// The same fp keeps its time; a new fp gets a strictly later second than the
// previous bytes of that key, even within the same second.
func (v *pageValidators) stamp(key string, fp uint64) time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	prev, ok := v.m[key]
	if ok && prev.fp == fp {
		return prev.since
	}
	now := v.now().UTC().Truncate(time.Second)
	if !ok && len(v.m) >= v.max {
		v.m = make(map[string]pageStamp)
		v.floor = now.Add(time.Second)
	}
	since := now
	if ok && prev.since.Add(time.Second).After(since) {
		since = prev.since.Add(time.Second)
	}
	if v.floor.After(since) {
		since = v.floor
	}
	v.m[key] = pageStamp{fp: fp, since: since}
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
