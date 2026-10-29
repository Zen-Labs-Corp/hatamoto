package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// applyPageFilter runs the render.page plugin chain over the view's sections
// and returns the cookies the plugins asked to set. The chain is fail open:
// whatever it cannot apply leaves the page as it was. Builder-only pages (a
// page_doc v1 cannot express) skip it.
func (h *renderHandlers) applyPageFilter(r *http.Request, path string, v *render.PageView) []sdk.SetCookie {
	if v.Doc != nil {
		return nil // builder-only page: the render.page contract is sections
	}
	in := sdk.PageContext{Path: path, Cookies: map[string]string{}, Blocks: make([]sdk.Block, 0, len(v.Sections))}
	for _, c := range r.Cookies() {
		// Only plugin-namespaced cookies enter the chain: never the session,
		// CSRF or any other core cookie.
		if !strings.HasPrefix(c.Name, pluginhost.CookiePrefix) {
			continue
		}
		if _, seen := in.Cookies[c.Name]; !seen {
			in.Cookies[c.Name] = c.Value
		}
	}
	if e := v.Entity; e != nil {
		in.Entity = &sdk.EntitySummary{ID: e.ID, SchemaSlug: e.Schema, Slug: e.Slug, Name: e.Title, Locale: firstNonEmpty(v.Locale, v.Site.Language)}
	}
	byID := map[string]render.Section{}
	for _, s := range v.Sections {
		in.Blocks = append(in.Blocks, sdk.Block{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, Hidden: s.IsVisible != nil && !*s.IsVisible})
		byID[s.ID] = s
	}
	out := h.pageFilter.Apply(r.Context(), in)

	sections := make([]render.Section, 0, len(out.Blocks))
	for i, b := range out.Blocks {
		var sec render.Section
		if b.ID != "" {
			sec = byID[b.ID] // a replaced block keeps the original's other fields
		}
		sec.ID, sec.Type, sec.Title, sec.Content, sec.SortOrder = b.ID, b.Type, b.Title, b.Content, i
		sections = append(sections, sec)
	}
	v.Sections = sections
	return out.SetCookies
}

// setPluginCookies writes the cookies a render.page plugin set. The names were
// checked against the manifest and namespaced (pluginhost.PluginCookieName); the
// attributes are fixed here.
func setPluginCookies(w http.ResponseWriter, r *http.Request, cookies []sdk.SetCookie) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	for _, c := range cookies {
		path := c.Path
		if path == "" {
			path = "/"
		}
		http.SetCookie(w, &http.Cookie{
			Name: c.Name, Value: c.Value, Path: path, MaxAge: c.MaxAge,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}
}
