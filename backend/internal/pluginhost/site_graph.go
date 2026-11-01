package pluginhost

import (
	"context"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// SiteGraphFilter adapts the render.site_graph hook chain to
// render.SiteGraphFilter. The render package decides what of the answer is
// kept (it never lets a filter change an @id, a @type or an existing key).
func SiteGraphFilter(h *hooks.Engine) render.SiteGraphFilter {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, c render.SiteGraphInput) render.SiteGraphInput {
		in := sdk.SiteGraph{Path: c.Path, Language: c.Language, SchemaSlug: c.SchemaSlug, EntityID: c.EntityID,
			Locale: c.Locale, Segment: c.Segment, Nodes: c.Nodes}
		out, ok := h.Apply(ctx, sdk.RenderSiteGraph.Name, in).(sdk.SiteGraph)
		if !ok {
			return c
		}
		c.Nodes = out.Nodes
		return c
	}
}
