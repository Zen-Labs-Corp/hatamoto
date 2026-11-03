package render

import (
	"encoding/json"
	"strings"
	"testing"
)

// currencyPage is a three-locale page: en-XX is the default, en-YY carries
// its own card currency, fr-ZZ carries none. The site services are priced in
// the site currency whatever locale is printed.
func currencyPage(locale string, shared bool, site string) *PageView {
	v := &PageView{
		BaseURL: "https://example.test/",
		Locale:  locale,
		Shared:  shared,
		Site:    SiteView{Name: "Example Co", Language: "en-XX"},
		Locales: []LocaleSummary{
			{Tag: "en-XX", Home: "https://example.test/", Default: true, Currency: "XXD"},
			{Tag: "en-YY", Segment: "yy", Home: "https://example.test/yy", Currency: "YYD"},
			{Tag: "fr-ZZ", Segment: "zz", Home: "https://example.test/zz"},
		},
	}
	v.Site.Org = OrgView{Currency: site, BusinessDetails: BusinessDetails{
		CatalogName: "Offers",
		Services:    []ServiceView{{Name: "Svc", Price: "900", PriceMin: "500", PriceMax: "1500"}},
	}}
	return v
}

// catalogCurrencies reads the first offer's currency and its price
// specification's currency, and fails when the catalogue has no offer.
func catalogCurrencies(t *testing.T, org map[string]any) (offer, spec string) {
	t.Helper()
	cat, _ := org["hasOfferCatalog"].(map[string]any)
	items, _ := cat["itemListElement"].([]any)
	if len(items) == 0 {
		t.Fatalf("catalogue has no offers: %v", org["hasOfferCatalog"])
	}
	o, _ := items[0].(map[string]any)
	offer, _ = o["priceCurrency"].(string)
	ps, _ := o["priceSpecification"].(map[string]any)
	spec, _ = ps["priceCurrency"].(string)
	return offer, spec
}

func TestOrgCatalogue_SiteServicesKeepSiteCurrency(t *testing.T) {
	tests := []struct {
		name  string
		page  *PageView
		offer string
		spec  string
	}{
		{"default locale", currencyPage("en-XX", false, "XXD"), "XXD", "XXD"},
		{"locale with own currency keeps site currency", currencyPage("en-YY", false, "XXD"), "XXD", "XXD"},
		{"locale without currency", currencyPage("fr-ZZ", false, "XXD"), "XXD", "XXD"},
		{"shared page keeps site currency", currencyPage("", true, "XXD"), "XXD", "XXD"},
		{"single locale keeps site currency", func() *PageView {
			v := currencyPage("en-YY", false, "XXD")
			v.Locales = v.Locales[1:2]
			return v
		}(), "XXD", "XXD"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			offer, spec := catalogCurrencies(t, orgOf(t, tc.page))
			if offer != tc.offer || spec != tc.spec {
				t.Fatalf("offer %q, spec %q; want %q, %q", offer, spec, tc.offer, tc.spec)
			}
		})
	}
}

func TestOrgCatalogue_NoSiteCurrencyNoKey(t *testing.T) {
	v := currencyPage("fr-ZZ", false, "")
	for i := range v.Locales {
		v.Locales[i].Currency = ""
	}
	if n := orgOf(t, v); n["hasOfferCatalog"] == nil {
		t.Fatal("no catalogue printed")
	}
	raw, err := json.Marshal(graphNodes(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "priceCurrency") {
		t.Fatalf("priceCurrency printed without any currency: %s", raw)
	}
}
