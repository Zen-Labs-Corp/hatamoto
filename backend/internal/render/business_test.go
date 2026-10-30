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
