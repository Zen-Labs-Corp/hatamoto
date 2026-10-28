package server

import (
	"context"
	"net/http"
	"regexp"

	"hatamoto.dev/core/internal/render"
)

// registryPathRe matches /plugins/<name> and /themes/<name>; it runs first so
// a path that cannot be a registry page costs nothing.
var registryPathRe = regexp.MustCompile(`^/(plugins|themes)/([a-z0-9][a-z0-9-]*)$`)

// registryRoute is a plugin or theme detail page: the verified registry
// entry's card for Name. Type is "plugin" or "theme"; Plugin or Theme is set.
type registryRoute struct {
	Type, Name string
	Plugin     *render.PluginCard
	Theme      *render.ThemeCard
}

// hostPageFunc resolves the published page at a listing path ("/plugins").
type hostPageFunc func(listPath string) (*routeResult, pageOutcome, error)

// hasDirectorySection reports whether sections (or the doc's components)
// include a visible directory of type typ ("plugin_directory" or "theme_directory").
func hasDirectorySection(sections []render.Section, typ string) bool {
	for _, sec := range sections {
		if sec.Type == typ && (sec.IsVisible == nil || *sec.IsVisible) {
			return true
		}
	}
	return false
}

// resolveRegistry maps /plugins/<name> or /themes/<name> to the registry entry
// of that name, only when the site's published /plugins (or /themes) page has
// the matching directory section. Nil means no such page (a 404).
func (s *Server) resolveRegistry(ctx context.Context, path string, r *http.Request) (*registryRoute, error) {
	return s.resolveRegistryWith(ctx, path, func(listPath string) (*routeResult, pageOutcome, error) {
		return s.resolveRouteForRender(ctx, listPath, r)
	})
}

func (s *Server) resolveRegistryWith(ctx context.Context, path string, hostPage hostPageFunc) (*registryRoute, error) {
	m := registryPathRe.FindStringSubmatch(path)
	if m == nil {
		return nil, nil
	}
	listPath, name := "/"+m[1], m[2]
	typ, dirType := "plugin", "plugin_directory"
	if m[1] == "themes" {
		typ, dirType = "theme", "theme_directory"
	}
	rr, _, err := hostPage(listPath)
	if err != nil {
		return nil, err
	}
	if rr == nil || rr.NotFound || rr.Redirect != nil || rr.Page == nil || rr.Page.Entity == nil {
		return nil, nil
	}
	e := rr.Page.Entity
	sections, doc := contentFromAttributes(e.Attributes, e.ID.String())
	if !hasDirectorySection(viewSections(sections, doc), dirType) {
		return nil, nil
	}
	vs := s.pluginDirectory().Views(ctx)
	route := &registryRoute{Type: typ, Name: name}
	switch typ {
	case "plugin":
		if vs.plugins.State != render.PluginDirectoryOK {
			return nil, nil
		}
		for i := range vs.plugins.Cards {
			if vs.plugins.Cards[i].Name == name {
				route.Plugin = &vs.plugins.Cards[i]
			}
		}
	default:
		if vs.themes.State != render.PluginDirectoryOK {
			return nil, nil
		}
		for i := range vs.themes.Cards {
			if vs.themes.Cards[i].Name == name {
				route.Theme = &vs.themes.Cards[i]
			}
		}
	}
	if route.Plugin == nil && route.Theme == nil {
		return nil, nil
	}
	return route, nil
}

// fillRegistry turns v into the detail page of rt: kind, card, SEO meta and
// breadcrumb. The page is indexable and, like the directory, cached for at
// most pluginDirectoryCacheAge.
func fillRegistry(v *render.PageView, rt *registryRoute) {
	rv := &render.RegistryPageView{Type: rt.Type, ListName: "Plugins", ListURL: "/plugins", Plugin: rt.Plugin, Theme: rt.Theme}
	var title, desc string
	if rt.Plugin != nil {
		title, desc = firstNonEmpty(rt.Plugin.Title, rt.Plugin.Name), rt.Plugin.Description
	} else {
		rv.ListName, rv.ListURL = "Themes", "/themes"
		title, desc = firstNonEmpty(rt.Theme.Title, rt.Theme.Name), rt.Theme.Description
	}
	v.Kind, v.Registry = render.KindRegistry, rv
	v.Page = render.PageMeta{Name: title, Title: title + " " + rt.Type, Description: desc, CanonicalURL: v.BaseURL + v.Path}
	v.Breadcrumb = []render.Crumb{{Name: rv.ListName, URL: rv.ListURL}, {Name: title}}
	v.MaxCacheAge = pluginDirectoryCacheAge
}
