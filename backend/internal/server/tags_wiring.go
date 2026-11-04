package server

import (
	"context"
	"html/template"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/tags"
)

// installTagsRender gives the page engine the enabled vendor tags. It fails
// closed: when the store cannot be read and nothing is cached, no tag loads.
func (s *Server) installTagsRender() {
	render.SetTags(func(ctx context.Context) (render.TagsConfig, bool) {
		if s.tagsStore == nil {
			return render.TagsConfig{}, false
		}
		set, err := s.tagsStore.Get(ctx)
		if err != nil {
			s.logger.Warn("tracking settings unreadable, loading no tags", "error", err)
			return render.TagsConfig{}, false
		}
		enabled := set.Enabled()
		if len(enabled) == 0 {
			return render.TagsConfig{}, false
		}
		banner, _ := s.consentState(ctx)
		cfg := render.TagsConfig{ExcludeSignedIn: set.ExcludeSignedIn, Member: auth.MemberMarkerName}
		var noscript []string
		for _, e := range enabled {
			cfg.Vendors = append(cfg.Vendors, render.TagVendor{
				ID: e.ID, Boot: e.Boot, Category: e.Category,
				Src: tags.Expand(e.Src, e.ValueID), Aux: e.AuxID, Value: e.ValueID,
			})
			// Cached HTML cannot vary by visitor, so the noscript fallback
			// exists only when the banner is off. With the banner on, a
			// category it does not ask is never allowed, so no-JS visitors
			// get no more than JS visitors do.
			if e.Noscript != "" && !banner {
				noscript = append(noscript, tags.Expand(e.Noscript, e.ValueID))
			}
		}
		cfg.Noscript = template.HTML(strings.Join(noscript, "")) // #nosec G203 -- vendor table templates with validated ids
		return cfg, true
	})
}

// consentState reports whether the consent banner is on and which categories
// it asks. An unreadable consent store counts as the default banner (the
// consent hook serves it); no store means no banner.
func (s *Server) consentState(ctx context.Context) (on bool, categories []string) {
	if s.consentStore == nil {
		return false, nil
	}
	set, err := s.consentStore.Get(ctx)
	if err != nil {
		set = consent.Defaults()
		set.Enabled = true
	}
	return set.Enabled, set.Categories
}

// tagsNeverLoad reports whether an enabled tag of this category can never
// load: the banner is on and does not ask for it, so it is never allowed.
func (s *Server) tagsNeverLoad(ctx context.Context) func(category string) bool {
	on, cats := s.consentState(ctx)
	return func(category string) bool { return on && !slices.Contains(cats, category) }
}

// tagsHosts are the CSP origins of the enabled vendors; none when nothing is
// on or the settings cannot be read.
func (s *Server) tagsHosts(ctx context.Context) cspExtras {
	if s.tagsStore == nil {
		return cspExtras{}
	}
	set, err := s.tagsStore.Get(ctx)
	if err != nil {
		return cspExtras{}
	}
	h := set.Hosts()
	return cspExtras{Script: h.Script, Connect: h.Connect, Style: h.Style, Font: h.Font}
}
