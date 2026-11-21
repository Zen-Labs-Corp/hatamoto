package server

import (
	"context"
	"net/http"

	"hatamoto.dev/core/internal/render"
)

// viewImageURLs are the image URLs the page's cards and entity header render,
// in page order; scheduled cards carry no image.
func viewImageURLs(v *render.PageView) []string {
	if v == nil {
		return nil
	}
	var out []string
	if v.Entity != nil && v.Entity.Image != "" {
		out = append(out, v.Entity.Image)
	}
	for _, it := range v.Items {
		if it.Image != "" {
			out = append(out, it.Image)
		}
	}
	return out
}

// buildPageViewPrefetched is buildPageView plus one variants lookup for every
// image the view already knows, so the srcset/picture calls that follow hit
// the cache instead of querying once per image.
func (s *Server) buildPageViewPrefetched(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
	v, err := s.buildPageView(ctx, r, rr)
	if err == nil && s.renderData != nil {
		if urls := viewImageURLs(v); len(urls) > 0 {
			s.renderData.PrefetchVariants(urls)
		}
	}
	return v, err
}
