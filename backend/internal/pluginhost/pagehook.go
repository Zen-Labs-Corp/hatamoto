package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// Limits on what a render.page filter may return.
const (
	maxPageBlocks = 200
	maxFieldLen   = 64 // longest string a slim block carries as a field
)

// hookFilter returns the filter adapter for a hook: render.page needs the
// plugin's manifest (declared cookies), every other filter is the generic one.
func (r *Runtime) hookFilter(m sdk.Manifest, hook, export string) func(context.Context, any) (any, error) {
	if hook == sdk.BuilderData.Name {
		return r.builderDataFilter(m, export)
	}
	generic := r.filter(m.Name, export)
	if hook == sdk.CommentCheckF.Name {
		return r.commentCheckFilter(m, generic)
	}
	if hook == sdk.RenderHead.Name {
		return noteRenderFailure(hook, r.headScriptGate(m.Name, generic))
	}
	if hook == sdk.RequestRedirect.Name {
		return r.redirectFilter(m, generic)
	}
	if hook != sdk.RenderPage.Name {
		return noteRenderFailure(hook, generic)
	}
	// A plugin that declared page_paths or page_path_prefixes is called only
	// there; every other page is the chain's value unchanged (no call, so no
	// failure to note).
	scope := newPageScope(m.PagePaths, m.PagePathPrefixes)
	return noteRenderFailure(hook, func(ctx context.Context, v any) (any, error) {
		pc, ok := v.(sdk.PageContext)
		if !ok || (!scope.everywhere() && !scope.matches(pc.Path)) {
			return v, nil
		}
		// The plugin sees only its own namespaced cookies it declared (under
		// the short name) and nothing set by earlier plugins; the chain keeps
		// the rest of the set. No other cookie is ever passed.
		in := pc
		in.Cookies = map[string]string{}
		for _, name := range m.Cookies {
			if val, ok := pc.Cookies[PluginCookieName(m.Name, name)]; ok {
				in.Cookies[name] = val
			}
		}
		// Likewise only the declared query keys; longer values are dropped,
		// never truncated.
		in.Query = make(map[string]string, len(m.PageQuery))
		for _, k := range m.PageQuery {
			if v, ok := pc.Query[k]; ok && len(v) <= sdk.MaxPageQueryValueBytes {
				in.Query[k] = v
			}
		}
		in.SetCookies, in.Hide = nil, nil
		if !m.FullPageBlocks() {
			in.Blocks = slimBlocks(pc.Blocks)
		}
		out, err := generic(ctx, in)
		if err != nil {
			return v, err
		}
		res, ok := out.(sdk.PageContext)
		if !ok {
			return v, nil
		}
		next, err := mergePageResult(pc, res, m)
		if err != nil {
			return v, fmt.Errorf("render.page result: %w", err)
		}
		return next, nil
	})
}

// renderTimeHooks are the filters that run while a page renders.
var renderTimeHooks = []string{sdk.RenderHead.Name, sdk.RenderSiteGraph.Name, sdk.RenderPage.Name, sdk.JSONLDEnrich.Name, sdk.BuilderData.Name}

// noteRenderFailure wraps a filter of a render-time hook so that any failure
// (error, timeout, oversized output, breaker skip; a deliberate rejection is
// not one) marks the render degraded and keeps its page out of the cache. The
// engine fails open, so the page is still served, just without that plugin's
// contribution.
func noteRenderFailure(hook string, f func(context.Context, any) (any, error)) func(context.Context, any) (any, error) {
	if !slices.Contains(renderTimeHooks, hook) {
		return f
	}
	return func(ctx context.Context, v any) (any, error) {
		out, err := f(ctx, v)
		if err != nil {
			var rej *sdk.Reject
			if !errors.As(err, &rej) {
				render.NoteDegradedCtx(ctx)
			}
		}
		return out, err
	}
}

// slimBlocks is what a plugin gets by default: id, type, title and hidden flag
// plus the few short scalar fields of sdk.PageBlockFields, never the content.
func slimBlocks(blocks []sdk.Block) []sdk.Block {
	out := make([]sdk.Block, len(blocks))
	for i, b := range blocks {
		out[i] = sdk.Block{ID: b.ID, Type: b.Type, Title: b.Title, Hidden: b.Hidden}
		for _, k := range sdk.PageBlockFields {
			switch v := b.Content[k].(type) {
			case string:
				if len(v) > maxFieldLen {
					continue
				}
			case bool, float64, int, int64:
			default:
				continue
			}
			if out[i].Fields == nil {
				out[i].Fields = map[string]any{}
			}
			out[i].Fields[k] = b.Content[k]
		}
	}
	return out
}

// mergePageResult applies a plugin's page result to the chain's value: its
// blocks replace the list (a slim plugin's blocks without content get the
// original content back by id), hidden ids drop out, and only declared cookie
// names may be set. Anything invalid fails the whole result (fail open: the page
// keeps what it had).
func mergePageResult(cur, res sdk.PageContext, m sdk.Manifest) (sdk.PageContext, error) {
	if len(res.Blocks) > maxPageBlocks {
		return cur, fmt.Errorf("more than %d blocks", maxPageBlocks)
	}
	if err := checkSetCookies(m, res.SetCookies); err != nil {
		return cur, err
	}
	next := cur
	byID := make(map[string]sdk.Block, len(cur.Blocks))
	for _, b := range cur.Blocks {
		byID[b.ID] = b
	}
	slim := !m.FullPageBlocks()
	next.Blocks = make([]sdk.Block, 0, len(res.Blocks))
	for _, b := range res.Blocks {
		if b.Hidden || slices.Contains(res.Hide, b.ID) {
			continue
		}
		b.Fields = nil
		// A block may not borrow another plugin's section type (its markup
		// is held to that plugin's grants, not this one's). A block the page
		// already had keeps its type.
		if orig, known := byID[b.ID]; !known || orig.Type != b.Type {
			if owner, ok := render.PluginSectionOwner(b.Type); ok && owner != m.Name {
				return cur, fmt.Errorf("block %q uses section type %q of plugin %q", b.ID, b.Type, owner)
			}
		}
		if slim {
			orig, known := byID[b.ID]
			switch {
			case known && len(b.Content) == 0:
				b = orig // untouched: the host re-attaches the original
			case b.Type == "" && known:
				b.Type = orig.Type
			case b.Type == "":
				return cur, fmt.Errorf("block %q has no type", b.ID)
			}
		}
		next.Blocks = append(next.Blocks, b)
	}
	next.SetCookies = slices.Clone(cur.SetCookies)
	for _, c := range res.SetCookies {
		c.Name = PluginCookieName(m.Name, c.Name)
		next.SetCookies = append(next.SetCookies, c)
	}
	next.Hide = nil
	return next, nil
}

// PageFilter applies the render.page chain. Cookies are the request's plugin
// cookies by browser name (hmp_<plugin>_<name>; core cookies are never put in).
type PageFilter struct {
	rt     *Runtime // nil: every registered filter counts as site-wide
	engine *hooks.Engine
}

// NewPageFilter returns the adapter over the runtime and its engine; nil
// engine gives nil. A nil runtime (engine-only callers) treats every
// registered filter as handling every page.
func NewPageFilter(rt *Runtime, engine *hooks.Engine) *PageFilter {
	if engine == nil {
		return nil
	}
	return &PageFilter{rt: rt, engine: engine}
}

// Active reports whether any plugin handles render.page; pages are then
// request-dependent and must not be shared-cached.
func (p *PageFilter) Active() bool { return p != nil && p.engine.HasFilter(sdk.RenderPage.Name) }

// Applies reports whether the render.page chain must run for path: a plugin
// that declared no page_paths or page_path_prefixes handles every page, one
// that did only its own.
// Such a page is request-dependent and must not be shared-cached.
func (p *PageFilter) Applies(path string) bool {
	if p == nil {
		return false
	}
	n := p.engine.FilterCount(sdk.RenderPage.Name)
	if p.rt == nil {
		return n > 0
	}
	return p.rt.pageFiltered(path, n)
}

// QueryKeys returns the query keys any loaded render.page plugin declared
// (page_query); the caller passes only those. Read-only; nil without a runtime.
func (p *PageFilter) QueryKeys() []string {
	if p == nil || p.rt == nil {
		return nil
	}
	if idx := p.rt.pageIdx.Load(); idx != nil {
		return idx.queryKeys
	}
	return nil
}

// Apply runs the chain over in and returns the result (in itself when nothing
// handled it or everything failed).
func (p *PageFilter) Apply(ctx context.Context, in sdk.PageContext) sdk.PageContext {
	if !p.Applies(in.Path) {
		return in
	}
	if out, ok := p.engine.Apply(ctx, sdk.RenderPage.Name, in).(sdk.PageContext); ok {
		return out
	}
	slog.Default().Warn("render.page chain returned an unexpected value")
	return in
}
