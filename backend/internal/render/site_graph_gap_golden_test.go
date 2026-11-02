package render

import "testing"

// gapGraphPage is a two-locale page: en-GB is the unprefixed default, fr-CA
// lives under /quebec. Both carry the catalogue, provider and property fields.
func gapGraphPage(locale string, shared bool) *PageView {
	v := &PageView{
		BaseURL: "https://example.test/",
		Locale:  locale,
		Shared:  shared,
		Site:    SiteView{Name: "Example Co", Language: "en-GB"},
		Locales: []LocaleSummary{
			{Tag: "en-GB", Home: "https://example.test/", Default: true},
			{Tag: "fr-CA", Segment: "quebec", Home: "https://example.test/quebec"},
		},
	}
	details := BusinessDetails{
		CatalogName:     "Offers",
		ServiceProvider: true,
		Services: []ServiceView{{
			Name: "Search optimisation", Description: "Monthly retainer", URL: "/services/seo",
			Price: "900", PriceMin: "500", PriceMax: "1500", UnitText: "MONTH",
		}},
	}
	props := []PropertyView{
		{Name: "Recommendation score", Value: "9.6", MinValue: "0", MaxValue: "10"},
		{Name: "Tier", Value: "Gold"},
	}
	v.Site.Org = OrgView{Currency: "GBP", BusinessDetails: details, AdditionalProperties: props}
	if !shared {
		v.Business = &BusinessView{
			ID: "locale", ServicesCurrency: "GBP", Address: Address{Street: "Example Street 1", City: "Sample City"},
			BusinessDetails: details, AdditionalProperties: props,
		}
	}
	return v
}

// TestSiteGraphGap pins the catalogue name, price range, provider, additional
// property and relative service URL output. Regenerate only with
// `go test -run TestSiteGraphGap -update`.
func TestSiteGraphGap(t *testing.T) {
	t.Run("org-locale", func(t *testing.T) {
		compareGraphGolden(t, "site-graph-gap-org-locale", graphJSON(t, gapGraphPage("fr-CA", false)))
	})
	t.Run("default-locale", func(t *testing.T) {
		compareGraphGolden(t, "site-graph-gap-default-locale", graphJSON(t, gapGraphPage("en-GB", false)))
	})
	t.Run("shared", func(t *testing.T) {
		compareGraphGolden(t, "site-graph-gap-shared", graphJSON(t, gapGraphPage("", true)))
	})
	t.Run("provider-off", func(t *testing.T) {
		v := gapGraphPage("fr-CA", false)
		for _, d := range []*BusinessDetails{&v.Site.Org.BusinessDetails, &v.Business.BusinessDetails} {
			d.ServiceProvider, d.CatalogName = false, ""
			d.Services = []ServiceView{{Name: "Search optimisation", URL: "https://other.example.test/seo", Price: "900"}}
		}
		compareGraphGolden(t, "site-graph-gap-provider-off", graphJSON(t, v))
	})
}
