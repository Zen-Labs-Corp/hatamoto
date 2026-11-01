package render

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// graphNodes marshals the site graph the way the head does and returns the
// nodes of its @graph.
func graphNodes(t *testing.T, v *PageView) []map[string]any {
	t.Helper()
	b, err := json.Marshal(siteGraphLD(v))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Graph
}

func nodeTypes(nodes []map[string]any) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n["@type"].(string))
	}
	return out
}

func businessPage(b *BusinessView) *PageView {
	return &PageView{
		BaseURL: "https://example.test/",
		Site: SiteView{
			Name: "Example Site", Phone: "site-phone", Hours: "site hours",
			Address: Address{Street: "Site Street", City: "Site City"},
		},
		Business: b,
	}
}

func TestSiteGraph_NoBusinessKeepsTwoNodes(t *testing.T) {
	nodes := graphNodes(t, businessPage(nil))
	if got := nodeTypes(nodes); !reflect.DeepEqual(got, []string{"Organization", "WebSite"}) {
		t.Fatalf("types = %v, want Organization, WebSite", got)
	}
}

func TestSiteGraph_LocalBusinessNode(t *testing.T) {
	v := businessPage(&BusinessView{
		ID:      "xx",
		Phone:   "own-phone",
		Address: Address{Street: "Example Street 1", City: "Sample City", PostalCode: "1000", Country: "EX"},
		POBox:   "Box 7", Lat: "-12.5", Lng: "33.25",
		MapURL: "https://example.test/map", GBPURL: "https://example.test/profile",
		OpeningHours: []HoursView{{Days: []string{"Monday", "Tuesday"}, Opens: "09:00", Closes: "17:00"}, {Opens: "01:00"}},
		SpecialHours: []SpecialHoursView{
			{From: "2030-12-24", To: "2030-12-26", Closed: true},
			{From: "2030-01-01", Opens: "10:00", Closes: "12:00"},
		},
		AreaServed: []string{"Sample Region"}, Currency: "USD", PriceRange: "$$",
		TaxID:         "T1",
		SameAs:        []string{"https://example.test/a", "https://example.test/profile"},
		ContactPoints: []ContactPointView{{Type: "sales", Phone: "555"}, {Type: "empty"}},
		Rating:        Rating{Value: "4.5", Count: "12"},
	})
	nodes := graphNodes(t, v)
	if got := nodeTypes(nodes); !reflect.DeepEqual(got, []string{"Organization", "LocalBusiness", "WebSite"}) {
		t.Fatalf("types = %v", got)
	}
	lb := nodes[1]
	if lb["@id"] != "https://example.test#localbusiness-xx" {
		t.Errorf("@id = %v", lb["@id"])
	}
	if p, _ := lb["parentOrganization"].(map[string]any); p["@id"] != "https://example.test#organization" {
		t.Errorf("parentOrganization = %v", lb["parentOrganization"])
	}
	if lb["name"] != "Example Site" {
		t.Errorf("name = %v, want the site name", lb["name"])
	}
	if lb["telephone"] != "own-phone" || lb["taxID"] != "T1" || lb["priceRange"] != "$$" || lb["currenciesAccepted"] != "USD" || lb["hasMap"] != "https://example.test/map" {
		t.Errorf("scalar fields = %v", lb)
	}
	addr, _ := lb["address"].(map[string]any)
	want := map[string]any{
		"@type": "PostalAddress", "streetAddress": "Example Street 1", "addressLocality": "Sample City",
		"postalCode": "1000", "addressCountry": "EX", "postOfficeBoxNumber": "Box 7",
	}
	if !reflect.DeepEqual(addr, want) {
		t.Errorf("address = %v, want %v", addr, want)
	}
	if geo, _ := lb["geo"].(map[string]any); geo["latitude"] != "-12.5" || geo["longitude"] != "33.25" || geo["@type"] != "GeoCoordinates" {
		t.Errorf("geo = %v", lb["geo"])
	}
	specs, _ := lb["openingHoursSpecification"].([]any)
	if len(specs) != 3 {
		t.Fatalf("openingHoursSpecification = %v, want 3 (the block without days is skipped)", specs)
	}
	reg := specs[0].(map[string]any)
	if !reflect.DeepEqual(reg["dayOfWeek"], []any{"Monday", "Tuesday"}) || reg["opens"] != "09:00" || reg["closes"] != "17:00" {
		t.Errorf("regular hours = %v", reg)
	}
	closed := specs[1].(map[string]any)
	if closed["validFrom"] != "2030-12-24" || closed["validThrough"] != "2030-12-26" || closed["opens"] != "00:00" || closed["closes"] != "00:00" {
		t.Errorf("closed special hours = %v", closed)
	}
	day := specs[2].(map[string]any)
	if day["validThrough"] != "2030-01-01" || day["opens"] != "10:00" {
		t.Errorf("single day special hours = %v", day)
	}
	if !reflect.DeepEqual(lb["sameAs"], []any{"https://example.test/a", "https://example.test/profile"}) {
		t.Errorf("sameAs = %v (the profile URL must not repeat)", lb["sameAs"])
	}
	if cps, _ := lb["contactPoint"].([]any); len(cps) != 1 {
		t.Errorf("contactPoint = %v, want the empty point dropped", lb["contactPoint"])
	}
	if r, _ := lb["aggregateRating"].(map[string]any); r["ratingValue"] != "4.5" || r["ratingCount"] != "12" {
		t.Errorf("aggregateRating = %v", lb["aggregateRating"])
	}
}

func TestSiteGraph_LocalBusinessTypeAndNameOverride(t *testing.T) {
	v := businessPage(&BusinessView{ID: "xx", Type: "Restaurant", Name: "Branch", Address: Address{City: "Sample City"}})
	lb := graphNodes(t, v)[1]
	if lb["@type"] != "Restaurant" || lb["name"] != "Branch" {
		t.Errorf("type/name = %v / %v", lb["@type"], lb["name"])
	}
	if org := graphNodes(t, v)[0]; org["name"] != "Example Site" {
		t.Errorf("the Organization name must stay the site name, got %v", org["name"])
	}
}

// The site-wide address, phone and hours feed the Organization only; the
// LocalBusiness prints exactly what its view holds.
func TestSiteGraph_LocalBusinessNeverReadsSiteContact(t *testing.T) {
	v := businessPage(&BusinessView{ID: "xx", Address: Address{City: "Sample City"}})
	lb := graphNodes(t, v)[1]
	raw, _ := json.Marshal(lb)
	for _, leak := range []string{"Site Street", "Site City", "site-phone", "site hours"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("LocalBusiness carries site-wide %q: %s", leak, raw)
		}
	}
	if _, ok := lb["telephone"]; ok {
		t.Errorf("telephone must be absent when the view has none")
	}
}

func TestSiteGraph_OrganizationPOBox(t *testing.T) {
	v := businessPage(nil)
	v.Site.Org = OrgView{POBox: "PO 12"}
	addr, _ := graphNodes(t, v)[0]["address"].(map[string]any)
	if addr["@type"] != "PostalAddress" || addr["postOfficeBoxNumber"] != "PO 12" {
		t.Errorf("address = %v, want a PostalAddress with the PO box", addr)
	}
	v.Site.Address = Address{City: "Sample City"}
	addr, _ = graphNodes(t, v)[0]["address"].(map[string]any)
	if addr["postOfficeBoxNumber"] != "PO 12" || addr["addressLocality"] != "Sample City" {
		t.Errorf("address = %v, want the PO box beside the city", addr)
	}
}

func TestSiteGraph_OrganizationExtrasOnlyWhenSet(t *testing.T) {
	bare := graphNodes(t, businessPage(nil))[0]
	for _, k := range []string{"legalName", "taxID", "faxNumber", "email", "aggregateRating", "sameAs"} {
		if _, ok := bare[k]; ok {
			t.Errorf("bare Organization has %q", k)
		}
	}

	v := businessPage(nil)
	v.Site.SocialLinks = []Link{{Label: "A", URL: "https://example.test/a"}}
	v.Site.Org = OrgView{
		LegalName: "Example Legal", TaxID: "T9", Fax: "fax", Email: "hello@example.test",
		SameAs: []string{"https://example.test/a", "https://example.test/b"},
		Rating: Rating{Value: "4", Count: "3"},
	}
	org := graphNodes(t, v)[0]
	if org["legalName"] != "Example Legal" || org["taxID"] != "T9" || org["faxNumber"] != "fax" || org["email"] != "hello@example.test" {
		t.Errorf("org = %v", org)
	}
	if !reflect.DeepEqual(org["sameAs"], []any{"https://example.test/a", "https://example.test/b"}) {
		t.Errorf("sameAs = %v, want social link first and no repeat", org["sameAs"])
	}
	if r, _ := org["aggregateRating"].(map[string]any); r["ratingValue"] != "4" {
		t.Errorf("aggregateRating = %v", org["aggregateRating"])
	}
}

func TestHeadGeoRegionMeta(t *testing.T) {
	v := businessPage(nil)
	v.GeoRegion = "ZZ-Q"
	out := renderHead(t, buildHeadView(context.Background(), v, nil))
	if !strings.Contains(out, `<meta name="geo.region" content="ZZ-Q">`) {
		t.Errorf("geo.region missing:\n%s", out)
	}

	for name, page := range map[string]*PageView{
		"no region value": businessPage(&BusinessView{ID: "xx", Address: Address{City: "Sample City"}}),
	} {
		out := renderHead(t, buildHeadView(context.Background(), page, nil))
		if strings.Contains(out, "geo.region") {
			t.Errorf("%s: geo.region must not print:\n%s", name, out)
		}
	}
}

func TestLocalBusinessOpeningHoursOmitEmptyTimes(t *testing.T) {
	v := businessPage(&BusinessView{
		ID: "xx", Address: Address{City: "Sample City"},
		OpeningHours: []HoursView{{Days: []string{"Monday"}}},
		SpecialHours: []SpecialHoursView{{From: "2030-01-01"}},
	})
	specs, _ := graphNodes(t, v)[1]["openingHoursSpecification"].([]any)
	if len(specs) != 2 {
		t.Fatalf("specs = %v", specs)
	}
	for _, s := range specs {
		m, _ := s.(map[string]any)
		for _, k := range []string{"opens", "closes"} {
			if _, ok := m[k]; ok {
				t.Errorf("%v has an empty %q key", m, k)
			}
		}
	}
}

// multiPage is a page of locale xx on a site with locales xx, yy and zz, where
// xx and zz have an office.
func multiPage(mode string) *PageView {
	v := businessPage(&BusinessView{
		ID: "xx", Phone: "xx-phone", VATID: "V-XX",
		Address: Address{Street: "Example Street 1", City: "Sample City"}, POBox: "Box 7",
	})
	v.Locale = "en-XX"
	v.Site.OrgAddress = mode
	v.Site.Language = "en-XX"
	v.Locales = []LocaleSummary{
		{Tag: "en-XX", CountryName: "Xland", Phone: "xx-phone", Language: "English", PriceRange: "$$", Currency: "XXD", HasOffice: true},
		{Tag: "en-YY", CountryName: "Yland", Language: "English"},
		{Tag: "fr-ZZ", CountryName: "Zland", Phone: "zz-phone", Language: "French",
			ContactPoints: []ContactPointView{{Name: "Sales", AreaServed: "Europe", AvailableLanguage: "Franglais"}}, HasOffice: true},
	}
	return v
}

func orgOf(t *testing.T, v *PageView) map[string]any {
	t.Helper()
	return graphNodes(t, v)[0]
}

func TestOrgNode_AddressSetting(t *testing.T) {
	tests := []struct {
		mode, locale  string
		office        bool
		wantStreet    string
		wantTelephone string
	}{
		{"", "xx", true, "Site Street", "site-phone"},
		{"locale", "xx", true, "Example Street 1", "xx-phone"},
		{"locale", "yy", false, "", ""},
		{"locale_or_site", "xx", true, "Example Street 1", "xx-phone"},
		{"locale_or_site", "yy", false, "Site Street", "site-phone"},
	}
	for _, tt := range tests {
		v := multiPage(tt.mode)
		if !tt.office {
			v.Business = nil
		}
		org := orgOf(t, v)
		addr, _ := org["address"].(map[string]any)
		street, _ := addr["streetAddress"].(string)
		tel, _ := org["telephone"].(string)
		if street != tt.wantStreet || tel != tt.wantTelephone {
			t.Errorf("mode %q office %v: street %q phone %q, want %q %q", tt.mode, tt.office, street, tel, tt.wantStreet, tt.wantTelephone)
		}
	}
	// A shared page and a single-locale page always print the site address.
	shared := multiPage("locale")
	shared.Shared, shared.Business = true, nil
	if addr, _ := orgOf(t, shared)["address"].(map[string]any); addr["streetAddress"] != "Site Street" {
		t.Errorf("shared page address = %v", addr)
	}
	single := multiPage("locale")
	single.Locales = nil
	if addr, _ := orgOf(t, single)["address"].(map[string]any); addr["streetAddress"] != "Site Street" {
		t.Errorf("single-locale address = %v", addr)
	}
}

func TestOrgNode_AreaServedAndContactPoints(t *testing.T) {
	org := orgOf(t, multiPage(""))
	wantArea := []any{
		map[string]any{"@type": "Country", "name": "Xland", "identifier": "XX"},
		map[string]any{"@type": "Country", "name": "Yland", "identifier": "YY"},
		map[string]any{"@type": "Country", "name": "Zland", "identifier": "ZZ"},
	}
	if !reflect.DeepEqual(org["areaServed"], wantArea) {
		t.Errorf("areaServed = %v", org["areaServed"])
	}
	points, _ := org["contactPoint"].([]any)
	if len(points) != 3 {
		t.Fatalf("contactPoint = %v, want 2 locale points and the site point", org["contactPoint"])
	}
	xx, zz, site := points[0].(map[string]any), points[1].(map[string]any), points[2].(map[string]any)
	if xx["telephone"] != "xx-phone" || xx["areaServed"] != "Xland" || xx["availableLanguage"] != "English" {
		t.Errorf("xx point = %v", xx)
	}
	if zz["name"] != "Sales" || zz["areaServed"] != "Europe" || zz["availableLanguage"] != "Franglais" {
		t.Errorf("zz point = %v", zz)
	}
	if site["telephone"] != "site-phone" || site["availableLanguage"] != "en-XX" {
		t.Errorf("site point = %v", site)
	}
	if org["priceRange"] != "$$" || org["currenciesAccepted"] != "XXD" || org["vatID"] != "V-XX" {
		t.Errorf("locale values: %v", org)
	}

	// One point only: an object, as on a single-locale site.
	v := multiPage("")
	v.Locales[0].Phone, v.Locales[2].Phone = "", ""
	if _, ok := orgOf(t, v)["contactPoint"].(map[string]any); !ok {
		t.Errorf("a single point must be an object: %v", orgOf(t, v)["contactPoint"])
	}

	// A shared page and a single-locale site print none of it.
	for name, page := range map[string]*PageView{"shared": multiPage(""), "single": multiPage("")} {
		if name == "shared" {
			page.Shared, page.Business = true, nil
		} else {
			page.Locales = nil
		}
		org := orgOf(t, page)
		for _, k := range []string{"areaServed", "image", "priceRange", "currenciesAccepted"} {
			if _, ok := org[k]; ok {
				t.Errorf("%s page prints %q", name, k)
			}
		}
		if _, ok := org["contactPoint"].(map[string]any); !ok {
			t.Errorf("%s page contactPoint = %v, want the site object", name, org["contactPoint"])
		}
	}
}

func TestOrgNode_ExtraFields(t *testing.T) {
	v := businessPage(nil)
	v.Site.LogoURL = "/media/logo.png"
	v.Site.Tagline = "Tagline"
	v.Site.Org = OrgView{
		Description: "About", AlternateName: "Alt", Slogan: "Slo", FoundingDate: "2009", VATID: "V9",
		EmployeesMin: 10, EmployeesMax: 50, LogoObject: true,
	}
	org := orgOf(t, v)
	if org["description"] != "About" || org["alternateName"] != "Alt" || org["slogan"] != "Slo" || org["foundingDate"] != "2009" || org["vatID"] != "V9" {
		t.Errorf("org = %v", org)
	}
	if n, _ := org["numberOfEmployees"].(map[string]any); n["@type"] != "QuantitativeValue" || n["minValue"] != float64(10) || n["maxValue"] != float64(50) {
		t.Errorf("numberOfEmployees = %v", org["numberOfEmployees"])
	}
	if org["logo"] != "https://example.test/media/logo.png" {
		t.Errorf("logo without dimensions must stay a string, got %v", org["logo"])
	}
	v.Site.LogoWidth, v.Site.LogoHeight = 640, 200
	logo, _ := orgOf(t, v)["logo"].(map[string]any)
	if logo["@type"] != "ImageObject" || logo["url"] != "https://example.test/media/logo.png" || logo["width"] != float64(640) || logo["height"] != float64(200) {
		t.Errorf("logo = %v", logo)
	}
	v.Site.Org.LogoObject = false
	if _, ok := orgOf(t, v)["logo"].(string); !ok {
		t.Errorf("logo with the setting off must be a string")
	}
	v.Site.Org.Description = ""
	if orgOf(t, v)["description"] != "Tagline" {
		t.Errorf("description must fall back to the tagline")
	}
}

func TestRatingLD(t *testing.T) {
	if got := ratingLD(Rating{Value: "4", Count: "12"}); !reflect.DeepEqual(got, map[string]any{"@type": "AggregateRating", "ratingValue": "4", "ratingCount": "12"}) {
		t.Errorf("default = %v", got)
	}
	got := ratingLD(Rating{Value: "4", Count: "12", Best: "5", Worst: "1", CountType: "reviews"})
	want := map[string]any{"@type": "AggregateRating", "ratingValue": "4", "bestRating": "5", "worstRating": "1", "reviewCount": float64(12)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviews = %v", got)
	}
	if b, _ := json.Marshal(got["reviewCount"]); string(b) != "12" {
		t.Errorf("reviewCount JSON = %s, want 12", b)
	}
	if got := ratingLD(Rating{Value: "4", Count: "0x10", CountType: "reviews"}); got["reviewCount"] != nil || got["ratingCount"] != nil {
		t.Errorf("bad review count printed: %v", got)
	}
}
