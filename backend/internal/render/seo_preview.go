package render

import "context"

// SEOMeta is one Open Graph or X tag: the property/name and its content.
type SEOMeta struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

// SEOPreviewTags is the SEO-derived part of a page's head: what a search
// engine or a link preview reads.
type SEOPreviewTags struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Canonical   string    `json:"canonical"`
	Robots      string    `json:"robots"`
	OG          []SEOMeta `json:"og"`
	Twitter     []SEOMeta `json:"twitter"`
}

// SEOPreview returns the title, description, canonical, robots and social
// tags the public head emits for v. It runs the same code as the head
// (seoHeadWith, which includes socialHead) without plugin head filters or the
// site graph, so the values are equal by construction. v is not modified.
func SEOPreview(ctx context.Context, v *PageView) SEOPreviewTags {
	h := seoHeadWith(ctx, v, true, nil)
	return SEOPreviewTags{
		Title:       h.Title,
		Description: h.Description,
		Canonical:   h.Canonical,
		Robots:      h.Robots,
		OG:          seoMetas(h.OG),
		Twitter:     seoMetas(h.Twitter),
	}
}

func seoMetas(in []metaPair) []SEOMeta {
	out := make([]SEOMeta, 0, len(in))
	for _, m := range in {
		out = append(out, SEOMeta{Key: m.Key, Content: m.Content})
	}
	return out
}
