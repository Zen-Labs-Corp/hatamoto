package server

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"net/http"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/httpx"
)

// encodedVariantFP returns the stored compressed form of a cached page for the
// coding the client negotiated, compressing the plain entry once on the first
// request. The plain entry stays the source of truth (the preloader reads it);
// variants are sibling keys under the same render: prefix, so a purge, the size
// cap and eviction treat them like any page. gen is the cache generation read
// before the plain entry was: a purge in between rejects the store. It returns
// no coding when the response should go out plain (switch off, demo, no
// compressible coding, body under the threshold, a refresh).
// The caller passes fingerprint(plain), so a response hashes its body once; a
// nil fp is computed lazily, after the early returns.
func (h *renderHandlers) encodedVariantFP(r *http.Request, key string, plain, fp []byte, gen uint64) (httpx.Encoding, []byte) {
	if h.encodeCached == nil || !h.encodeCached() || refreshRequested(r.Context()) {
		return httpx.EncodingNone, nil
	}
	return cachedVariantFP(r, h.cache, key, plain, fp, h.pageCacheTTL(), gen, h.logger.Warn)
}

// A stored variant is fingerprint(plain) + compressed bytes. The plain entry
// can be rewritten (a short MaxCacheAge, a preload refresh, expiry) without
// touching its variants, so a variant is served only while the fingerprint of
// the plain body it was made from still matches the body being served.
const fingerprintLen = 8

func fingerprintSum(plain []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(plain)
	return h.Sum64()
}

func fingerprint(plain []byte) []byte {
	return binary.BigEndian.AppendUint64(nil, fingerprintSum(plain))
}

// cachedVariant is the lookup-or-compress step shared by pages and feeds.
func cachedVariant(r *http.Request, c htmlCache, key string, plain []byte, ttl time.Duration, gen uint64, warn func(string, ...any)) (httpx.Encoding, []byte) {
	return cachedVariantFP(r, c, key, plain, nil, ttl, gen, warn)
}

// cachedVariantFP is cachedVariant with the caller's fingerprint(plain)
// (nil = compute it here).
func cachedVariantFP(r *http.Request, c htmlCache, key string, plain, fp []byte, ttl time.Duration, gen uint64, warn func(string, ...any)) (httpx.Encoding, []byte) {
	enc := httpx.NegotiateEncoding(r)
	if enc == httpx.EncodingNone || len(plain) < httpx.CompressMinSize {
		return httpx.EncodingNone, nil
	}
	ctx := r.Context()
	vkey := cache.EncodedKey(key, string(enc))
	if fp == nil {
		fp = fingerprint(plain)
	}
	if stored, err := c.Get(ctx, vkey); err == nil && len(stored) > fingerprintLen && bytes.HasPrefix(stored, fp) {
		return enc, stored[fingerprintLen:]
	}
	packed, err := httpx.CompressBytes(enc, plain, true)
	if err != nil {
		warn("render: compress cached page failed", "error", err)
		return httpx.EncodingNone, nil
	}
	// fp may alias the caller's memory: build the value in a fresh slice.
	val := append(append(make([]byte, 0, len(fp)+len(packed)), fp...), packed...)
	if _, err := c.SetIfGeneration(ctx, vkey, val, ttl, gen); err != nil {
		warn("render: cache set failed", "error", err)
	}
	return enc, packed
}
