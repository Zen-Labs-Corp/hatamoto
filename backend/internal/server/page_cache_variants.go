package server

import (
	"net/http"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/httpx"
)

// encodedVariant returns the stored compressed form of a cached page for the
// coding the client negotiated, compressing the plain entry once on the first
// request. The plain entry stays the source of truth (the preloader reads it);
// variants are sibling keys under the same render: prefix, so a purge, the size
// cap and eviction treat them like any page. gen is the cache generation read
// before the plain entry was: a purge in between rejects the store. It returns
// no coding when the response should go out plain (switch off, demo, no
// compressible coding, body under the threshold, a refresh).
func (h *renderHandlers) encodedVariant(r *http.Request, key string, plain []byte, gen uint64) (httpx.Encoding, []byte) {
	if h.encodeCached == nil || !h.encodeCached() || refreshRequested(r.Context()) {
		return httpx.EncodingNone, nil
	}
	return cachedVariant(r, h.cache, key, plain, h.pageCacheTTL(), gen, h.logger.Warn)
}

// cachedVariant is the lookup-or-compress step shared by pages and feeds.
func cachedVariant(r *http.Request, c htmlCache, key string, plain []byte, ttl time.Duration, gen uint64, warn func(string, ...any)) (httpx.Encoding, []byte) {
	enc := httpx.NegotiateEncoding(r)
	if enc == httpx.EncodingNone || len(plain) < httpx.CompressMinSize {
		return httpx.EncodingNone, nil
	}
	ctx := r.Context()
	vkey := cache.EncodedKey(key, string(enc))
	if packed, err := c.Get(ctx, vkey); err == nil && len(packed) > 0 {
		return enc, packed
	}
	packed, err := httpx.CompressBytes(enc, plain, true)
	if err != nil {
		warn("render: compress cached page failed", "error", err)
		return httpx.EncodingNone, nil
	}
	if _, err := c.SetIfGeneration(ctx, vkey, packed, ttl, gen); err != nil {
		warn("render: cache set failed", "error", err)
	}
	return enc, packed
}
