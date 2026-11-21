package server

import (
	"maps"
	"slices"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

// siteDerived is what buildSiteView decodes from one site config. A
// *siteconfig.Config is never edited once handed out (the store replaces the
// pointer on write and on refresh), so it is decoded once per pointer.
type siteDerived struct {
	cfg        *siteconfig.Config
	theme      siteconfig.ThemeConfig
	social     []render.Link
	contact    render.ContactFloat
	address    render.Address
	org        render.OrgView
	orgErr     error
	orgAddress string
	settings   map[string]any
	cssName    string
	emailPlain bool
	design     builder.Design
	designErr  error
}

func newSiteDerived(cfg *siteconfig.Config) *siteDerived {
	d := &siteDerived{
		cfg:        cfg,
		theme:      effectiveTheme(cfg),
		social:     socialLinks(cfg.ThemeExtra),
		contact:    contactFloat(cfg),
		address:    postalAddress(cfg.PostalAddress),
		orgAddress: orgAddressMode(cfg),
		settings:   siteSettings(cfg.ThemeExtra),
		cssName:    render.CustomCSSName(render.AdditionalCSS(cfg.ThemeExtra)),
		emailPlain: !siteconfig.ParseProtectEmail(cfg.ThemeExtra),
	}
	d.org, d.orgErr = orgView(cfg)
	d.design, d.designErr = builder.ParseDesign(cfg.ThemeExtra)
	return d
}

// siteDerivedFor returns the memo for cfg, decoding on a new pointer.
func (s *Server) siteDerivedFor(cfg *siteconfig.Config) *siteDerived {
	if d := s.siteDerived.Load(); d != nil && d.cfg == cfg {
		return d
	}
	d := newSiteDerived(cfg)
	s.siteDerived.Store(d)
	return d
}

// apply fills the site-config-derived fields of site with copies of the
// memoised values: templates and hooks may hold or edit what they get.
func (d *siteDerived) apply(site *render.SiteView) {
	site.SocialLinks = slices.Clone(d.social)
	site.ContactFloat = d.contact
	site.Address = d.address
	site.OrgAddress = d.orgAddress
	site.Org = cloneOrg(d.org)
	site.Settings, _ = cloneSettings(d.settings).(map[string]any)
	site.CustomCSSName = d.cssName
	site.EmailPlain = d.emailPlain
	site.Design = cloneDesign(d.design)
}

// themeCopy is the theme with its own HeaderCTA.
func (d *siteDerived) themeCopy() siteconfig.ThemeConfig {
	t := d.theme
	if t.HeaderCTA != nil {
		c := *t.HeaderCTA
		t.HeaderCTA = &c
	}
	return t
}

// cloneSettings deep-copies a decoded JSON value (maps, slices, scalars).
func cloneSettings(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneSettings(e)
		}
		return out
	case []any:
		if x == nil {
			return x
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneSettings(e)
		}
		return out
	}
	return v
}

func cloneDesign(d builder.Design) builder.Design {
	d.Tokens.Color = maps.Clone(d.Tokens.Color)
	d.Tokens.Space = maps.Clone(d.Tokens.Space)
	d.Tokens.FS = maps.Clone(d.Tokens.FS)
	d.Tokens.Shadow = maps.Clone(d.Tokens.Shadow)
	d.Parts = maps.Clone(d.Parts)
	if d.Presets != nil {
		presets := make(map[string]builder.Preset, len(d.Presets))
		for k, p := range d.Presets {
			if p.S != nil {
				s := make(map[string]map[string]string, len(p.S))
				for bp, kv := range p.S {
					s[bp] = maps.Clone(kv)
				}
				p.S = s
			}
			presets[k] = p
		}
		d.Presets = presets
	}
	return d
}

func cloneOrg(o render.OrgView) render.OrgView {
	o.SameAs = slices.Clone(o.SameAs)
	o.AdditionalProperties = slices.Clone(o.AdditionalProperties)
	o.Founders = slices.Clone(o.Founders)
	o.Memberships = slices.Clone(o.Memberships)
	o.Brands = slices.Clone(o.Brands)
	o.PaymentMethods = slices.Clone(o.PaymentMethods)
	o.KnowsAbout = slices.Clone(o.KnowsAbout)
	o.Awards = slices.Clone(o.Awards)
	o.Services = slices.Clone(o.Services)
	return o
}
