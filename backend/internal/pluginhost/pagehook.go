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
	maxPageBlocks  = 200
	maxSetCookies  = 8
	maxCookieValue = 2048
	maxCookieAge   = 365 * 24 * 3600
	maxFieldLen    = 64 // longest string a slim block carries as a field
)

// CookiePrefix starts the name of every cookie a plugin may read or set.
const CookiePrefix = "hmp_"

// PluginCookieName is the browser cookie name of a plugin's cookie: plugins
// are confined to the namespace hmp_<plugin>_<name>, so they can neither read
// nor set the session cookie, the CSRF cookie or another plugin's cookies.
// The plugin itself only ever sees and uses <name>.
func PluginCookieName(plugin, name string) string { return CookiePrefix + plugin + "_" + name }

// hookFilter returns the filter adapter for a hook: render.page needs the
// plugin's manifest (declared cookies), every other filter is the generic one.
func (r *Runtime) hookFilter(m sdk.Manifest, hook, export string) func(context.Context, any) (any, error) {
	if hook == sdk.BuilderData.Name {
		return r.builderDataFilter(m, export)
	}
	generic := r.filter(m.Name, export)
	if hook == sdk.RenderHead.Name {
		return noteRenderFailure(hook, r.headScriptGate(m.Name, generic))
	}
	if hook != sdk.RenderPage.Name {
		return noteRenderFailure(hook, generic)
	}
	return noteRenderFailure(hook, func(ctx context.Context, v any) (any, error) {
		pc, ok := v.(sdk.PageContext)
		if !ok {
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
var renderTimeHooks = []string{sdk.RenderHead.Name, sdk.RenderPage.Name, sdk.JSONLDEnrich.Name, sdk.BuilderData.Name}

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
	if len(res.SetCookies) > maxSetCookies {
		return cur, fmt.Errorf("more than %d cookies", maxSetCookies)
	}
	for _, c := range res.SetCookies {
		if !slices.Contains(m.Cookies, c.Name) {
			return cur, fmt.Errorf("cookie %q is not declared in the manifest", c.Name)
		}
		if len(c.Value) > maxCookieValue || c.MaxAge < 0 || c.MaxAge > maxCookieAge {
			return cur, fmt.Errorf("cookie %q: value over %d bytes or max_age outside 0..%d", c.Name, maxCookieValue, maxCookieAge)
		}
		if !validCookieValue(c.Value) {
			return cur, fmt.Errorf("cookie %q: value has characters outside the cookie-safe set", c.Name)
		}
		if c.Path != "" && (c.Path[0] != '/' || len(c.Path) > 256) {
			return cur, fmt.Errorf("cookie %q: bad path", c.Name)
		}
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

func validCookieValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x20 || c >= 0x7f || c == '"' || c == ',' || c == ';' || c == '\\' {
			return false
		}
	}
	return true
}

// PageFilter applies the render.page chain. Cookies are the request's plugin
// cookies by browser name (hmp_<plugin>_<name>; core cookies are never put in).
type PageFilter struct{ engine *hooks.Engine }

// NewPageFilter returns the adapter over engine; nil engine gives nil.
func NewPageFilter(engine *hooks.Engine) *PageFilter {
	if engine == nil {
		return nil
	}
	return &PageFilter{engine: engine}
}

// Active reports whether any plugin handles render.page; pages are then
// request-dependent and must not be shared-cached.
func (p *PageFilter) Active() bool { return p != nil && p.engine.HasFilter(sdk.RenderPage.Name) }

// Apply runs the chain over in and returns the result (in itself when nothing
// handled it or everything failed).
func (p *PageFilter) Apply(ctx context.Context, in sdk.PageContext) sdk.PageContext {
	if !p.Active() {
		return in
	}
	if out, ok := p.engine.Apply(ctx, sdk.RenderPage.Name, in).(sdk.PageContext); ok {
		return out
	}
	slog.Default().Warn("render.page chain returned an unexpected value")
	return in
}
