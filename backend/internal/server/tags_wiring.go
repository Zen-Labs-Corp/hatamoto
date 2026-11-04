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
		asked := s.consentAsks(ctx)
		cfg := render.TagsConfig{ExcludeSignedIn: set.ExcludeSignedIn, Member: auth.MemberMarkerName}
		var noscript []string
		for _, e := range enabled {
			cfg.Vendors = append(cfg.Vendors, render.TagVendor{
				ID: e.ID, Boot: e.Boot, Category: e.Category,
				Src: tags.Expand(e.Src, e.ValueID), Aux: e.AuxID, Value: e.ValueID,
			})
			// Cached HTML cannot vary by visitor, so the noscript fallback
			// exists only when no visitor can be asked for this category.
			if e.Noscript != "" && !asked(e.Category) {
				noscript = append(noscript, tags.Expand(e.Noscript, e.ValueID))
			}
		}
		cfg.Noscript = template.HTML(strings.Join(noscript, "")) // #nosec G203 -- vendor table templates with validated ids
		return cfg, true
	})
}

// consentAsks reports whether a visitor can be asked for a category: the
// banner is on and the category is one of its asked ones. An unreadable
// consent store counts as asking (the consent hook serves the default banner).
func (s *Server) consentAsks(ctx context.Context) func(category string) bool {
	if s.consentStore == nil {
		return func(string) bool { return false }
	}
	set, err := s.consentStore.Get(ctx)
	if err != nil {
		set = consent.Defaults()
		set.Enabled = true
	}
	return func(category string) bool {
		return set.Enabled && slices.Contains(set.Categories, category)
	}
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
