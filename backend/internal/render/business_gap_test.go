package render

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// gapPage is a two-locale page: en-XX is the unprefixed default, fr-ZZ sits
// under /zz.
func gapPage(locale string, shared bool) *PageView {
	v := businessPage(nil)
	v.Locale = locale
	v.Shared = shared
	v.Locales = []LocaleSummary{
		{Tag: "en-XX", Home: "https://example.test/", Default: true},
		{Tag: "fr-ZZ", Segment: "zz", Home: "https://example.test/zz"},
	}
	return v
}

func catalogOf(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	cat, ok := node["hasOfferCatalog"].(map[string]any)
	if !ok {
		t.Fatalf("no hasOfferCatalog: %v", node)
	}
	return cat
}

func firstOffer(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	offers, _ := catalogOf(t, node)["itemListElement"].([]any)
	if len(offers) == 0 {
		t.Fatalf("no offers: %v", node)
	}
	return offers[0].(map[string]any)
}

func TestServiceURLResolution(t *testing.T) {
	const base = "https://example.test"
	oneLocale := businessPage(nil)
	tests := []struct {
		name string
		v    *PageView
		raw  string
		want string
	}{
		{"prefixed locale", gapPage("fr-ZZ", false), "/services/seo", "https://example.test/zz/services/seo"},
		{"unprefixed default locale", gapPage("en-XX", false), "/services/seo", "https://example.test/services/seo"},
		{"shared page uses the default locale", gapPage("", true), "/services/seo", "https://example.test/services/seo"},
		{"one-locale site", oneLocale, "/services/seo", "https://example.test/services/seo"},
		{"absolute unchanged", gapPage("fr-ZZ", false), "https://other.example.test/x", "https://other.example.test/x"},
		{"protocol-relative unchanged", gapPage("fr-ZZ", false), "//example.test/x", "//example.test/x"},
		{"backslash unchanged", gapPage("fr-ZZ", false), `/\example.test`, `/\example.test`},
		{"empty", gapPage("fr-ZZ", false), "", ""},
	}
	for _, tt := range tests {
		if got := serviceURL(tt.v, base, tt.raw); got != tt.want {
			t.Errorf("%s: serviceURL = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestOfferCatalogNameDefault(t *testing.T) {
	v := businessPage(nil)
	v.Site.Org.Services = []ServiceView{{Name: "Repair"}}
	if got := catalogOf(t, orgOf(t, v))["name"]; got != "Services" {
		t.Errorf("default name = %v", got)
	}
	v.Site.Org.CatalogName = "Offers"
	if got := catalogOf(t, orgOf(t, v))["name"]; got != "Offers" {
		t.Errorf("name = %v", got)
	}
}

func TestOfferPriceSpecification(t *testing.T) {
	spec := func(s ServiceView, currency string) any {
		v := businessPage(nil)
		v.Site.Org.Services = []ServiceView{s}
		v.Site.Org.Currency = currency
		return firstOffer(t, orgOf(t, v))["priceSpecification"]
	}
	if got, want := spec(ServiceView{Name: "A", PriceMin: "10"}, "XXD"), map[string]any{
		"@type": "UnitPriceSpecification", "minPrice": 10.0, "priceCurrency": "XXD"}; !reflect.DeepEqual(got, want) {
		t.Errorf("min only = %v", got)
	}
	if got, want := spec(ServiceView{Name: "A", PriceMax: "20.5"}, "XXD"), map[string]any{
		"@type": "UnitPriceSpecification", "maxPrice": 20.5, "priceCurrency": "XXD"}; !reflect.DeepEqual(got, want) {
		t.Errorf("max only = %v", got)
	}
	if got, want := spec(ServiceView{Name: "A", PriceMin: "10", PriceMax: "20", UnitText: "HOUR"}, ""), map[string]any{
		"@type": "UnitPriceSpecification", "minPrice": 10.0, "maxPrice": 20.0, "unitText": "HOUR"}; !reflect.DeepEqual(got, want) {
		t.Errorf("both, no currency = %v", got)
	}
	if got := spec(ServiceView{Name: "A", PriceMin: "n/a", UnitText: "HOUR"}, "XXD"); got != nil {
		t.Errorf("unparsable range printed: %v", got)
	}
	v := businessPage(nil)
	v.Site.Org.Services = []ServiceView{{Name: "A", Price: "5", PriceMin: "1"}}
	offer := firstOffer(t, orgOf(t, v))
	if offer["price"] != 5.0 || offer["priceSpecification"] == nil {
		t.Errorf("price and range must coexist: %v", offer)
	}
}

func TestServiceProviderOnlyWhenOn(t *testing.T) {
	v := businessPage(nil)
	v.Site.Org.Services = []ServiceView{{Name: "Repair"}}
	svc := firstOffer(t, orgOf(t, v))["itemOffered"].(map[string]any)
	if _, ok := svc["provider"]; ok {
		t.Errorf("provider printed while off: %v", svc)
	}
	v.Site.Org.ServiceProvider = true
	svc = firstOffer(t, orgOf(t, v))["itemOffered"].(map[string]any)
	if want := map[string]any{"@id": "https://example.test#organization"}; !reflect.DeepEqual(svc["provider"], want) {
		t.Errorf("provider = %v, want %v", svc["provider"], want)
	}
}

func TestServiceURLInGraph(t *testing.T) {
	v := gapPage("fr-ZZ", false)
	v.Business = &BusinessView{ID: "zz"}
	v.Business.Services = []ServiceView{{Name: "SEO", URL: "/services/seo"}}
	lb := graphNodes(t, v)[1]
	svc := firstOffer(t, lb)["itemOffered"].(map[string]any)
	if svc["url"] != "https://example.test/zz/services/seo" {
		t.Errorf("url = %v", svc["url"])
	}
}

func TestOrgAdditionalProperty(t *testing.T) {
	v := businessPage(nil)
	v.Site.Org.AdditionalProperties = []PropertyView{
		{Name: "Recommendation score", Value: "9.6", MinValue: "0", MaxValue: "10", UnitText: "POINTS"},
		{Name: "Tier", Value: "Gold"},
		{Value: "nameless"},
	}
	want := []any{
		map[string]any{"@type": "PropertyValue", "name": "Recommendation score", "value": 9.6, "minValue": 0.0, "maxValue": 10.0, "unitText": "POINTS"},
		map[string]any{"@type": "PropertyValue", "name": "Tier", "value": "Gold"},
	}
	if got := orgOf(t, v)["additionalProperty"]; !reflect.DeepEqual(got, want) {
		t.Errorf("additionalProperty = %v", got)
	}

	// A card's resolved list wins on its locale page.
	lp := gapPage("fr-ZZ", false)
	lp.Site.Org.AdditionalProperties = v.Site.Org.AdditionalProperties
	lp.Business = &BusinessView{ID: "zz"}
	for i := range lp.Locales {
		lp.Locales[i].AdditionalProperties = []PropertyView{{Name: "Tier", Value: "Silver"}}
	}
	got, _ := orgOf(t, lp)["additionalProperty"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["value"] != "Silver" {
		t.Errorf("card override = %v", got)
	}
	// A shared page keeps the site-wide list.
	sp := gapPage("", true)
	sp.Site.Org.AdditionalProperties = v.Site.Org.AdditionalProperties
	if got, _ := orgOf(t, sp)["additionalProperty"].([]any); len(got) != 2 {
		t.Errorf("shared page = %v", got)
	}
}

func TestLocalBusinessNoAdditionalProperty(t *testing.T) {
	v := gapPage("fr-ZZ", false)
	v.Business = &BusinessView{ID: "zz", AdditionalProperties: []PropertyView{{Name: "Tier", Value: "Gold"}}}
	if _, ok := graphNodes(t, v)[1]["additionalProperty"]; ok {
		t.Error("LocalBusiness must not print additionalProperty")
	}
}

func TestEmptyGapFieldsAddNoKeys(t *testing.T) {
	v := gapPage("fr-ZZ", false)
	v.Business = &BusinessView{ID: "zz"}
	v.Site.Org.BusinessDetails = extendedDetails()
	v.Business.BusinessDetails = extendedDetails()
	b, err := json.Marshal(siteGraphLD(v))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"additionalProperty", "priceSpecification", "provider"} {
		if bytesContains(b, `"`+key+`"`) {
			t.Errorf("%s printed for empty fields: %s", key, b)
		}
	}
}

func bytesContains(b []byte, s string) bool { return len(s) > 0 && strings.Contains(string(b), s) }
