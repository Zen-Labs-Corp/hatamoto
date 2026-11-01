package server

import (
	"context"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/media"
)

// logoSearch lists media rows whose name contains search.
type logoSearch func(ctx context.Context, search string) ([]*media.Media, error)

// logoSearch looks names up in the media library; nil without one.
func (s *Server) logoSearch() logoSearch {
	if s.mediaService == nil {
		return nil
	}
	return func(ctx context.Context, search string) ([]*media.Media, error) {
		res, err := s.mediaService.List(ctx, &media.ListMediaFilter{Search: search}, 20, 0)
		if err != nil {
			return nil, err
		}
		return res.Items, nil
	}
}

// logoCacheTTL bounds how long a looked-up logo size is reused; a media Update
// or Delete drops the entries at once, an Upload (which does not notify) waits
// for the TTL.
const logoCacheTTL = 5 * time.Minute

type logoSizeEntry struct {
	w, h    int
	expires time.Time
}

// logoSizeCache remembers logo sizes by logo URL, so a render does not search
// the media library every time (the search is an unindexed scan). The zero
// value is ready to use.
type logoSizeCache struct {
	mu sync.Mutex
	m  map[string]logoSizeEntry
}

// size is logoSize through the cache; lookup errors are not remembered.
func (c *logoSizeCache) size(ctx context.Context, on bool, search logoSearch, logoURL string) (w, h int) {
	if !on || search == nil {
		return 0, 0
	}
	c.mu.Lock()
	e, ok := c.m[logoURL]
	c.mu.Unlock()
	if ok && time.Now().Before(e.expires) {
		return e.w, e.h
	}
	failed := false
	w, h = logoDimensionsWith(ctx, func(ctx context.Context, q string) ([]*media.Media, error) {
		items, err := search(ctx, q)
		failed = err != nil
		return items, err
	}, logoURL)
	if !failed {
		c.mu.Lock()
		if c.m == nil || len(c.m) > 64 {
			c.m = make(map[string]logoSizeEntry)
		}
		c.m[logoURL] = logoSizeEntry{w, h, time.Now().Add(logoCacheTTL)}
		c.mu.Unlock()
	}
	return w, h
}

func (c *logoSizeCache) reset() {
	c.mu.Lock()
	c.m = nil
	c.mu.Unlock()
}

// logoSize is the logo's pixel size when the owner asked for an ImageObject
// logo (on), else 0, 0 without any lookup.
func logoSize(ctx context.Context, on bool, search logoSearch, logoURL string) (w, h int) {
	if !on || search == nil {
		return 0, 0
	}
	return logoDimensionsWith(ctx, search, logoURL)
}

// logoDimensionsWith is the pixel size of the site logo found in the media
// library, or 0, 0 when the logo is not a library file, the size is unknown (an
// SVG has none) or the lookup fails: the Organization then keeps the plain URL.
func logoDimensionsWith(ctx context.Context, search logoSearch, logoURL string) (w, h int) {
	p := urlPath(logoURL)
	if p == "" || strings.HasSuffix(p, "/") {
		return 0, 0
	}
	items, err := search(ctx, path.Base(p))
	if err != nil {
		return 0, 0
	}
	for _, m := range items {
		if m == nil || !logoMatches(p, m) {
			continue
		}
		if m.Width != nil && m.Height != nil && *m.Width > 0 && *m.Height > 0 {
			return *m.Width, *m.Height
		}
		return 0, 0
	}
	return 0, 0
}

// logoMatches reports whether the logo path p points at media row m: p ends
// with the row's public path or its storage path.
func logoMatches(p string, m *media.Media) bool {
	if m.PublicURL != nil {
		if pub := urlPath(*m.PublicURL); pub != "" && strings.HasSuffix(p, pub) {
			return true
		}
	}
	sp := strings.Trim(m.StoragePath, "/")
	return sp != "" && strings.HasSuffix(p, "/"+sp)
}

// urlPath is the path of a site URL (absolute, protocol-relative or relative),
// without query or fragment; "" when it cannot be parsed.
func urlPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Path
}
