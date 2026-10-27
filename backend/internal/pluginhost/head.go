package pluginhost

import (
	"context"
	"maps"
	"slices"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// HeadFilter adapts the render.head hook chain to render.HeadFilter. The
// render package sanitises and escapes whatever tags come back.
func HeadFilter(h *hooks.Engine) render.HeadFilter {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, c render.HeadContext) render.HeadContext {
		in := sdk.HeadContext{Path: c.Path, Language: c.Language, SchemaSlug: c.SchemaSlug, EntityID: c.EntityID}
		out, ok := h.Apply(ctx, sdk.RenderHead.Name, in).(sdk.HeadContext)
		if !ok {
			return c
		}
		c.ReplaceSiteGraph = c.ReplaceSiteGraph || out.ReplaceSiteGraph
		for _, t := range out.Tags {
			c.Tags = append(c.Tags, render.HeadTag{Kind: t.Kind, Attrs: t.Attrs, JSONLD: t.JSONLD})
		}
		return c
	}
}

// headScriptGate drops the script-src tags a render.head filter adds while the
// owner has not granted the plugin site.scripts, and notes that it did so (the
// admin shows a banner). Tags that were already in the chain are kept.
func (r *Runtime) headScriptGate(plugin string, f func(context.Context, any) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, v any) (any, error) {
		out, err := f(ctx, v)
		hc, ok := out.(sdk.HeadContext)
		if err != nil || !ok || r.scriptsAllowed(plugin) {
			return out, err
		}
		before, _ := v.(sdk.HeadContext)
		kept := make([]sdk.HeadTag, 0, len(hc.Tags))
		for _, t := range hc.Tags {
			if t.Kind == "script-src" && !slices.ContainsFunc(before.Tags, func(b sdk.HeadTag) bool { return maps.Equal(b.Attrs, t.Attrs) && b.Kind == t.Kind }) {
				r.scriptsDropped.Store(plugin, true)
				continue
			}
			kept = append(kept, t)
		}
		hc.Tags = kept
		return hc, nil
	}
}

// ScriptsDropped reports whether the plugin has asked for a script tag that was
// dropped because it lacks the site.scripts grant.
func (r *Runtime) ScriptsDropped(plugin string) bool {
	_, ok := r.scriptsDropped.Load(plugin)
	return ok
}

// SetScriptsDeny installs a policy that refuses a plugin's scripts whatever is
// granted (a stored grant outlives a policy change); the server wires it.
func (r *Runtime) SetScriptsDeny(f func(plugin string) bool) { r.scriptsDeny.Store(&f) }

// scriptsAllowed is the site.scripts grant, counted only while the loaded
// manifest still requests it (a grant row outlives an update that drops it).
func (r *Runtime) scriptsAllowed(plugin string) bool {
	if f := r.scriptsDeny.Load(); f != nil && (*f)(plugin) {
		return false
	}
	m, ok := r.manifest(plugin)
	return ok && slices.Contains(m.Permissions, sdk.PermSiteScripts) && r.caps.grants.Has(plugin, sdk.PermSiteScripts)
}
