package render

import (
	"context"
	"time"
)

// Host-side types of the render.site_graph filter. They mirror the plugin SDK's
// payload; the plugin host adapts SDK values to these, so this package stays
// free of SDK imports.

// SiteGraphInput is the render.site_graph filter input and output: the nodes of
// the page's site graph and where the page is. The filter can add keys to a
// node and append nodes; mergeSiteGraph decides what survives.
type SiteGraphInput struct {
	Path       string
	Language   string
	Locale     string
	Segment    string
	SchemaSlug string
	EntityID   string
	Nodes      []map[string]any
}

// SiteGraphFilter transforms the site graph; it is fail-open (see
// applySiteGraphFilter).
type SiteGraphFilter func(ctx context.Context, in SiteGraphInput) SiteGraphInput

// siteGraphFilterTimeout bounds the filter chain, like headFilterTimeout.
var siteGraphFilterTimeout = 2 * time.Second

// applySiteGraphFilter runs f with a deadline. A nil filter, a panic or a
// timeout returns no nodes (fail-open: nothing to merge); the last two also mark
// the render degraded so the page is not cached. A timed-out filter may still be
// writing to the maps it was given, so they are never read again.
func applySiteGraphFilter(ctx context.Context, f SiteGraphFilter, in SiteGraphInput) SiteGraphInput {
	if f == nil {
		return in
	}
	ctx, cancel := context.WithTimeout(ctx, siteGraphFilterTimeout)
	defer cancel()
	done := make(chan SiteGraphInput, 1)
	go func() {
		defer func() {
			if recover() != nil {
				NoteDegradedCtx(ctx)
				done <- SiteGraphInput{}
			}
		}()
		done <- f(ctx, in)
	}()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		NoteDegradedCtx(ctx)
		return SiteGraphInput{}
	}
}

// extendSiteGraph passes the nodes of the core graph doc through f and merges
// the answer back into doc's @graph. A nil filter returns doc untouched.
func extendSiteGraph(ctx context.Context, f SiteGraphFilter, v *PageView, doc map[string]any) map[string]any {
	if f == nil {
		return doc
	}
	list, _ := doc["@graph"].([]any)
	core := make([]map[string]any, 0, len(list))
	for _, n := range list {
		if m, ok := n.(map[string]any); ok {
			core = append(core, m)
		}
	}
	out := applySiteGraphFilter(ctx, f, SiteGraphInput{
		Path: v.Path, Language: v.Site.Language, Locale: v.Locale, Segment: v.Segment,
		SchemaSlug: v.SchemaSlug, EntityID: v.EntityID, Nodes: cloneNodes(core),
	})
	merged := mergeSiteGraph(core, out.Nodes)
	graph := make([]any, len(merged))
	for i, n := range merged {
		graph[i] = n
	}
	return map[string]any{"@context": doc["@context"], "@graph": graph}
}

// mergeSiteGraph applies a filter's answer to the core nodes. A plugin node
// with the @id of a core node may add keys the core node lacks; it can never
// change @id or @type, nor remove or overwrite a core key. A plugin node with a
// new @id is appended, unless it repeats an @id already seen; a node without a
// string @id is dropped. Nodes are matched by @id only, never by position. The
// core nodes are not modified: a node that gains keys is a shallow copy, so core
// values are never re-encoded.
func mergeSiteGraph(core, plugin []map[string]any) []map[string]any {
	out := append([]map[string]any(nil), core...)
	index := make(map[string]int, len(core))
	for i, n := range core {
		if id, ok := n["@id"].(string); ok {
			index[id] = i
		}
	}
	copied := make(map[int]bool)
	appended := make(map[string]bool)
	for _, p := range plugin {
		id, ok := p["@id"].(string)
		if !ok || id == "" {
			continue
		}
		i, known := index[id]
		if appended[id] {
			continue
		}
		if !known {
			appended[id] = true
			index[id] = len(out)
			copied[len(out)] = true
			out = append(out, shallowCopy(p))
			continue
		}
		for k, val := range p {
			if k == "@id" || k == "@type" {
				continue
			}
			if _, exists := out[i][k]; exists {
				continue
			}
			if !copied[i] {
				out[i] = shallowCopy(out[i])
				copied[i] = true
			}
			out[i][k] = val
		}
	}
	return out
}

func shallowCopy(m map[string]any) map[string]any {
	c := make(map[string]any, len(m)+1)
	for k, v := range m {
		c[k] = v
	}
	return c
}

func cloneNodes(nodes []map[string]any) []map[string]any {
	out := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		out[i] = cloneJSON(n).(map[string]any)
	}
	return out
}

// cloneJSON deep-copies the value kinds a graph node holds, so a filter can
// never reach the core maps.
func cloneJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(x))
		for k, e := range x {
			c[k] = cloneJSON(e)
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = cloneJSON(e)
		}
		return c
	case []string:
		return append([]string(nil), x...)
	default:
		return v
	}
}
