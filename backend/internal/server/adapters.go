package server

import (
	"context"

	"hatamoto.dev/core/internal/siteconfig"
)

// tenantLookupAdapter implements auth.TenantLookup against hatamoto_site_config.
// The name comes from site_config.company_name and the slug is the stable
// canonical derivative.
type tenantLookupAdapter struct {
	siteConfig *siteconfig.Store
}

// canonicalTenantSlug is the deterministic slug for the site. The value is
// stable and only appears in API responses and UI display. Populated from
// cfg.Hatamoto.CanonicalSlug in server.New() — fail-fast validation in
// config.Validate() guarantees the underlying HATAMOTO_CANONICAL_SLUG env var
// is non-empty before the server boots.
//
// Package-level var (rather than a Server field) so any adapter can read it without a struct hop.
var canonicalTenantSlug string

// GetTenantNameSlug returns (company_name, canonical_slug) for the login response.
// CompanyName falls back to "Hatamoto" when siteconfig is unset / unreadable
// so login keeps working before an admin fills the site-config form.
// The slug has no in-code fallback — startup-time validation guarantees
// canonicalTenantSlug is non-empty by the time any request reaches this method.
func (a *tenantLookupAdapter) GetTenantNameSlug(ctx context.Context) (string, string, error) {
	if a.siteConfig == nil {
		return "Hatamoto", canonicalTenantSlug, nil
	}
	cfg, err := a.siteConfig.Get(ctx)
	if err != nil {
		return "Hatamoto", canonicalTenantSlug, nil
	}
	name := cfg.CompanyName
	if name == "" {
		name = "Hatamoto"
	}
	return name, canonicalTenantSlug, nil
}
