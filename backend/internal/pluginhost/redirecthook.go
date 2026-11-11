package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

const (
	// redirectTimeout bounds one request.redirect call as a whole, including
	// the wait for a free instance and a cold start: the request context has
	// no deadline, and this runs in front of every request to a declared path.
	redirectTimeout = time.Second
	// maxRedirectLocation caps the Location a plugin may return.
	maxRedirectLocation = 2048
)

// redirectFilter is the request.redirect adapter for one plugin. The engine
// calls every plugin's filter for the hook, so each adapter answers only the
// paths its plugin owns. The plugin sees only the cookies it declared (by
// short name), never the output fields, and its answer must be a 302 or 307
// to a same-site location plus declared cookies; anything else is an error,
// which the engine logs and answers by keeping the request as it was.
func (r *Runtime) redirectFilter(m sdk.Manifest, generic func(context.Context, any) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, v any) (any, error) {
		rc, ok := v.(sdk.RedirectContext)
		if !ok || r.redirectOwner(rc.Path) != m.Name {
			return v, nil
		}
		in := rc
		in.Cookies = map[string]string{}
		for _, name := range m.Cookies {
			if val, ok := rc.Cookies[PluginCookieName(m.Name, name)]; ok {
				in.Cookies[name] = val
			}
		}
		in.Redirect, in.SetCookies = nil, nil
		out, err := generic(ctx, in)
		if err != nil {
			return v, err
		}
		res, ok := out.(sdk.RedirectContext)
		if !ok {
			return v, nil
		}
		if err := checkRedirect(m, res); err != nil {
			return v, fmt.Errorf("request.redirect result: %w", err)
		}
		next := rc
		next.Redirect = res.Redirect
		for _, c := range res.SetCookies {
			c.Name = PluginCookieName(m.Name, c.Name)
			next.SetCookies = append(next.SetCookies, c)
		}
		return next, nil
	}
}

func checkRedirect(m sdk.Manifest, res sdk.RedirectContext) error {
	if rd := res.Redirect; rd != nil {
		if rd.Status != 302 && rd.Status != 307 {
			return fmt.Errorf("status %d is not 302 or 307", rd.Status)
		}
		if len(rd.Location) > maxRedirectLocation || !strictRedirectLocation(rd.Location) {
			return fmt.Errorf("location is not a same-site path")
		}
	}
	return checkSetCookies(m, res.SetCookies)
}

// strictRedirectLocation is safeLocation plus the checks a request.redirect
// answer needs because core sends the exact string as the Location header. The
// path (before ? or #) must hold no backslash and no encoded slash or
// backslash, and must not start with // or /\ after the dot-segment removal a
// browser (or path.Clean) does: "/./\x" and "/.//x" both end up as "//x".
func strictRedirectLocation(l string) bool {
	if !safeLocation(l) {
		return false
	}
	p := l
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	low := strings.ToLower(p)
	if strings.Contains(p, "\\") || strings.Contains(low, "%2f") || strings.Contains(low, "%5c") {
		return false
	}
	low = strings.ReplaceAll(low, "%2e", ".")
	if strings.HasPrefix(path.Clean(low), "//") || strings.HasPrefix(removeDotSegments(low), "//") {
		return false
	}
	return true
}

// removeDotSegments drops "." and ".." segments the way a URL parser does,
// keeping empty segments (path.Clean would merge them).
func removeDotSegments(p string) string {
	var out []string
	segs := strings.Split(p, "/")
	for i, s := range segs {
		switch s {
		case ".":
			if i == len(segs)-1 {
				out = append(out, "")
			}
		case "..":
			if len(out) > 1 {
				out = out[:len(out)-1]
			}
			if i == len(segs)-1 {
				out = append(out, "")
			}
		default:
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

// redirectOwner returns the plugin that owns the request.redirect path, or "".
func (r *Runtime) redirectOwner(path string) string {
	if idx := r.redirectOwners.Load(); idx != nil {
		return (*idx)[path]
	}
	return ""
}

// claimRedirectPaths records m's declared paths after a load. The first plugin
// loaded keeps a path; a later one declaring it is logged and ignored for it
// (install refuses this; a development directory or a stale store can still
// produce it). Only a plugin that registered the hook owns paths.
func (r *Runtime) claimRedirectPaths(m sdk.Manifest, hooksGot []string) {
	r.redirectMu.Lock()
	defer r.redirectMu.Unlock()
	next := r.withoutRedirectOwner(m.Name)
	if slices.Contains(hooksGot, sdk.RequestRedirect.Name) {
		for _, p := range m.RedirectPaths {
			if owner, taken := next[p]; taken {
				r.log.Warn("request.redirect path already owned by another plugin; ignored",
					slog.String("plugin", m.Name), slog.String("path", p), slog.String("owner", owner))
				continue
			}
			next[p] = m.Name
		}
	}
	r.redirectOwners.Store(&next)
}

// releaseRedirectPaths drops the paths of an unloaded plugin and lets the
// remaining loaded plugins claim any of them they had lost.
func (r *Runtime) releaseRedirectPaths(name string) {
	r.redirectMu.Lock()
	defer r.redirectMu.Unlock()
	next := r.withoutRedirectOwner(name)
	r.mu.Lock()
	sts := slices.Clone(r.statuses)
	r.mu.Unlock()
	for _, st := range sts {
		if !st.Loaded || !slices.Contains(st.Hooks, sdk.RequestRedirect.Name) {
			continue
		}
		for _, p := range st.Manifest.RedirectPaths {
			if _, taken := next[p]; !taken {
				next[p] = st.Name
			}
		}
	}
	r.redirectOwners.Store(&next)
}

// withoutRedirectOwner copies the index minus the paths owned by name.
func (r *Runtime) withoutRedirectOwner(name string) map[string]string {
	next := map[string]string{}
	if cur := r.redirectOwners.Load(); cur != nil {
		maps.Copy(next, *cur)
	}
	maps.DeleteFunc(next, func(_, owner string) bool { return owner == name })
	return next
}

// RedirectFilter applies the request.redirect chain to a request for a path a
// plugin declared.
type RedirectFilter struct {
	rt     *Runtime
	engine *hooks.Engine
}

// NewRedirectFilter returns the adapter over the runtime and its engine; a nil
// runtime or engine gives nil.
func NewRedirectFilter(rt *Runtime, e *hooks.Engine) *RedirectFilter {
	if rt == nil || e == nil {
		return nil
	}
	return &RedirectFilter{rt: rt, engine: e}
}

// Declares reports whether a plugin owns the exact path. It is a lock-free
// map lookup, cheap enough to run on every request.
func (f *RedirectFilter) Declares(path string) bool {
	return f != nil && f.rt.redirectOwner(path) != ""
}

// Apply asks the owner of in.Path. in.Cookies holds the request's plugin cookies
// by browser name (hmp_<plugin>_<name>); the adapter hands each plugin only
// its declared ones, by short name. answered is true when the plugin returned a
// redirect and/or cookies (already validated and namespaced); on any failure,
// or an empty answer, it is false and the caller handles the request as usual.
func (f *RedirectFilter) Apply(ctx context.Context, in sdk.RedirectContext) (out sdk.RedirectContext, answered bool) {
	if f == nil {
		return in, false
	}
	ctx, cancel := context.WithTimeout(ctx, redirectTimeout)
	defer cancel()
	res, ok := f.engine.Apply(ctx, sdk.RequestRedirect.Name, in).(sdk.RedirectContext)
	if !ok || (res.Redirect == nil && len(res.SetCookies) == 0) {
		return in, false
	}
	return res, true
}
