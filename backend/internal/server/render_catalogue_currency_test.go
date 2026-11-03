package server

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
)

// catalogueCurrencies reads the first offer's currency and its price
// specification's currency from the named node's offer catalogue. ok is false
// when the node or its catalogue is absent.
func catalogueCurrencies(graph, node string, t *testing.T) (offer, spec string, ok bool) {
	t.Helper()
	cat, _ := nodes(t, graph)[node]["hasOfferCatalog"].(map[string]any)
	items, _ := cat["itemListElement"].([]any)
	if len(items) == 0 {
		return "", "", false
	}
	o, _ := items[0].(map[string]any)
	offer, _ = o["priceCurrency"].(string)
	ps, _ := o["priceSpecification"].(map[string]any)
	spec, _ = ps["priceCurrency"].(string)
	return offer, spec, true
}

func catalogueFixture(t *testing.T) map[string]string {
	t.Helper()
	profile := `{"name":"Example Legal","services":[{"name":"Svc","price":"900","price_min":"500","price_max":"1500"}]}`
	c := foldBase0(profile, `{"currency":"XXD"}`)
	withYYD := strings.Replace(c.cards, `"fr-CA":{`, `"fr-CA":{"currency":"YYD",`, 1)
	if withYYD == c.cards {
		t.Fatal("fixture did not change: fr-CA card not found to add the YYD currency")
	}
	c.cards = withYYD
	cfg := foldConfig(t, c.config())
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		t.Fatal(err)
	}
	if site.Currency != "XXD" {
		t.Fatalf("site profile currency %q after the fold, want XXD", site.Currency)
	}
	return pageGraphs(t, cfg, foldSet(t, true), false)
}

// The catalogue lists the site's services, priced in the site currency: a
// locale card's own currency never relabels them.
func TestOrgCatalogue_SiteServicesKeepSiteCurrency(t *testing.T) {
	t.Parallel()
	pages := catalogueFixture(t)
	for _, page := range []string{"shared", "default", "fr-CA", "de-XX"} {
		offer, spec, ok := catalogueCurrencies(pages[page], "Organization", t)
		if !ok {
			t.Errorf("%s: Organization has no catalogue offers", page)
			continue
		}
		if offer != "XXD" || spec != "XXD" {
			t.Errorf("%s: offer %q, spec %q; want XXD", page, offer, spec)
		}
	}
}

// Where a page also prints a LocalBusiness carrying the inherited site
// services, both nodes price the same service in the same currency.
func TestOrgCatalogue_AgreesWithLocalBusinessAfterFold(t *testing.T) {
	t.Parallel()
	pages := catalogueFixture(t)
	compared := map[string]bool{}
	for page, graph := range pages {
		lbOffer, lbSpec, ok := catalogueCurrencies(graph, "LocalBusiness", t)
		if !ok {
			continue
		}
		offer, spec, ok := catalogueCurrencies(graph, "Organization", t)
		if !ok {
			t.Errorf("%s: LocalBusiness has offers but Organization has none", page)
			continue
		}
		compared[page] = true
		if offer != lbOffer || spec != lbSpec {
			t.Errorf("%s: Organization %q/%q, LocalBusiness %q/%q", page, offer, spec, lbOffer, lbSpec)
		}
	}
	if !compared["fr-CA"] {
		t.Fatalf("fr-CA page not compared (compared: %v); agreement not exercised where the card currency differs", compared)
	}
}
