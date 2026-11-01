package server

import (
	"context"
	"net/url"
	"path"
	"strings"

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
