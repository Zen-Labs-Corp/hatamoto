package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

func businessSet(t *testing.T, prefixAll bool, entries ...entity.LocaleEntry) *entity.LocaleSet {
	t.Helper()
	set, err := entity.NewLocaleSet("en", entries, prefixAll, "")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func businessConfig(locales string) *siteconfig.Config {
	return &siteconfig.Config{
		Phone:           "site-phone",
		BusinessHours:   "site hours",
		PostalAddress:   json.RawMessage(`{"street":"Site Street","city":"Site City","country":"SC"}`),
		BusinessProfile: json.RawMessage(`{"name":"Example Legal","tax_id":"T1","email":"site@example.test","sameas":["https://example.test/site"],"rating":{"value":"4","count":"9"}}`),
		LocaleProfiles:  json.RawMessage(locales),
	}
}

const cardsJSON = `{
  "fr": {"address":{"street":"Example Street 1","city":"Sample City"},"email":"card@example.test","business_type":"Restaurant"},
  "nl": {"no_office":true,"address":{"street":"Example Street 2"}},
  "en": {"address":{"state":"S","postal_code":"1"},"phone":"own"},
  "xx": {"address":{"street":"Orphan Street"}}
}`

func TestLocaleBusiness(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"},
		entity.LocaleEntry{Tag: "nl", Segment: "nl"}, entity.LocaleEntry{Tag: "it", Segment: "it"})
	cfg := businessConfig(cardsJSON)

	b, err := localeBusiness(cfg, set, "fr")
	if err != nil || b == nil {
		t.Fatalf("fr: %+v, %v", b, err)
	}
	if b.ID != "fr" || b.Type != "Restaurant" || b.Address.Street != "Example Street 1" || b.Address.City != "Sample City" {
		t.Errorf("fr card = %+v", b)
	}
	if b.Phone != "site-phone" || b.TaxID != "T1" || b.Hours != "site hours" || b.Rating.Value != "4" {
		t.Errorf("fr did not fall back to the site-wide values: %+v", b)
	}
	if b.Email != "card@example.test" {
		t.Errorf("fr email = %q, want the card's", b.Email)
	}
	if b.Address.Country != "" {
		t.Errorf("site-wide country %q leaked into the card's address", b.Address.Country)
	}

	for name, loc := range map[string]string{
		"no office":                "nl",
		"card without street/city": "en",
		"locale without a card":    "it",
		"orphan card":              "xx",
		"shared tag":               entity.LocaleShared,
	} {
		if got, err := localeBusiness(cfg, set, loc); err != nil || got != nil {
			t.Errorf("%s: got %+v, %v; want nil", name, got, err)
		}
	}
}

func TestLocaleBusiness_IDOfDefaultLocaleWithoutSegment(t *testing.T) {
	set, err := entity.NewLocaleSet("en-GB", []entity.LocaleEntry{{Tag: "en-GB"}, {Tag: "fr", Segment: "fr"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig(`{"en-GB":{"address":{"city":"Sample City"}}}`)
	b, err := localeBusiness(cfg, set, "en-GB")
	if err != nil || b == nil || b.ID != "en-gb" {
		t.Fatalf("got %+v, %v; want id en-gb (the default locale has no segment)", b, err)
	}
}

func TestLocaleBusiness_SingleLocaleAndBadData(t *testing.T) {
	cards := `{"en":{"address":{"city":"Sample City"}}}`
	one := businessSet(t, false, entity.LocaleEntry{Tag: "en"})
	if b, err := localeBusiness(businessConfig(cards), one, "en"); err != nil || b != nil {
		t.Errorf("one locale: %+v, %v; want nil", b, err)
	}
	if b, err := localeBusiness(businessConfig(cards), nil, "en"); err != nil || b != nil {
		t.Errorf("nil set: %+v, %v; want nil", b, err)
	}
	two := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	if b, err := localeBusiness(businessConfig(`[1]`), two, "en"); err == nil || b != nil {
		t.Errorf("bad locale map: %+v, %v; want an error", b, err)
	}
	bad := businessConfig(cards)
	bad.BusinessProfile = json.RawMessage(`{"name":`)
	if b, err := localeBusiness(bad, two, "en"); err == nil || b != nil {
		t.Errorf("bad site profile: %+v, %v; want an error", b, err)
	}
}

func TestIsSharedPage(t *testing.T) {
	shared := &entity.Entity{Locale: entity.LocaleShared}
	fr := &entity.Entity{Locale: "fr"}
	tests := []struct {
		name string
		rr   *routeResult
		ent  *entity.Entity
		want bool
	}{
		{"shared entity under a locale prefix", &routeResult{Locale: "fr"}, shared, true},
		{"shared entity unprefixed", &routeResult{Locale: "en"}, shared, true},
		{"locale entity", &routeResult{Locale: "fr"}, fr, false},
		{"locale entity under prefix-all root", &routeResult{Locale: entity.LocaleShared}, fr, false},
		{"archive in a locale", &routeResult{Locale: "fr"}, nil, false},
		{"archive in the default locale", &routeResult{Locale: "en"}, nil, false},
		{"archive under prefix-all root", &routeResult{Locale: entity.LocaleShared}, nil, true},
		{"no locale scope", &routeResult{}, nil, true},
	}
	for _, tt := range tests {
		if got := isSharedPage(tt.rr, tt.ent); got != tt.want {
			t.Errorf("%s: isSharedPage = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The resolver's locale scope, which isSharedPage relies on for pages without an
// entity (archives, search, the themed 404): a prefixed or unprefixed
// default-locale path carries its locale, an unprefixed path under prefix-all is
// shared.
func TestIsSharedPage_RouteScopes(t *testing.T) {
	tests := []struct {
		name      string
		prefixAll bool
		entries   []entity.LocaleEntry
		path      string
		wantLoc   string
		shared    bool
	}{
		{"default locale unprefixed", false, fixtureLocales, "/missing", "en", false},
		{"prefixed locale", false, fixtureLocales, "/fr/missing", "fr", false},
		{"prefix-all unprefixed", true, []entity.LocaleEntry{{Tag: "en", Segment: "en"}, {Tag: "fr", Segment: "fr"}}, "/missing", entity.LocaleShared, true},
	}
	for _, tt := range tests {
		f := newLocaleRouteFixture(t, tt.prefixAll, tt.entries...)
		rr, _, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, tt.path, httptest.NewRequest("GET", "/x", nil))
		if err != nil || rr == nil {
			t.Fatalf("%s: %+v, %v", tt.name, rr, err)
		}
		if rr.Locale != tt.wantLoc || isSharedPage(rr, nil) != tt.shared {
			t.Errorf("%s: Locale = %q shared = %v, want %q %v", tt.name, rr.Locale, isSharedPage(rr, nil), tt.wantLoc, tt.shared)
		}
	}
}

func TestOrgView(t *testing.T) {
	got, err := orgView(businessConfig(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := render.OrgView{
		LegalName: "Example Legal", TaxID: "T1", Email: "site@example.test",
		SameAs: []string{"https://example.test/site"}, Rating: render.Rating{Value: "4", Count: "9"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orgView = %+v, want %+v", got, want)
	}
	if empty, err := orgView(&siteconfig.Config{}); err != nil || !reflect.DeepEqual(empty, render.OrgView{}) {
		t.Errorf("orgView of an empty config = %+v, %v; want the zero value", empty, err)
	}
	bad := businessConfig(`{}`)
	bad.BusinessProfile = json.RawMessage(`{"name":`)
	withExtras := func(profile string) render.OrgView {
		cfg := businessConfig(`{}`)
		cfg.BusinessProfile = json.RawMessage(profile)
		got, err := orgView(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := withExtras(`{"sameas":["https://example.test/a"],"address":{"po_box":"PO 12","gbp_url":"https://example.test/gbp"}}`); got.POBox != "PO 12" ||
		!reflect.DeepEqual(got.SameAs, []string{"https://example.test/a", "https://example.test/gbp"}) {
		t.Errorf("orgView extras = %+v", got)
	}
	if got := withExtras(`{"sameas":["https://example.test/gbp"],"address":{"gbp_url":"https://example.test/gbp"}}`); !reflect.DeepEqual(got.SameAs, []string{"https://example.test/gbp"}) {
		t.Errorf("duplicate profile address: SameAs = %v", got.SameAs)
	}
	if got := withExtras(`{"address":{"gbp_url":"javascript:alert(1)"}}`); got.SameAs != nil {
		t.Errorf("non-http profile address: SameAs = %v", got.SameAs)
	}
	if _, err := orgView(bad); err == nil {
		t.Error("orgView of invalid JSON: want an error")
	}
}

func TestOrgView_NewFields(t *testing.T) {
	cfg := businessConfig(`{}`)
	cfg.BusinessProfile = json.RawMessage(`{"description":"About us","alternate_name":"Example","slogan":"Be sample","founding_date":"2009-05","employees_min":10,"employees_max":50,"vat_id":"V1","logo_object":true,"rating":{"value":"4.5","count":"7","best":"5","worst":"1","count_type":"reviews"}}`)
	got, err := orgView(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := render.OrgView{
		Description: "About us", AlternateName: "Example", Slogan: "Be sample", FoundingDate: "2009-05",
		EmployeesMin: 10, EmployeesMax: 50, VATID: "V1", LogoObject: true,
		Rating: render.Rating{Value: "4.5", Count: "7", Best: "5", Worst: "1", CountType: "reviews"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orgView = %+v, want %+v", got, want)
	}
	cfg.BusinessProfile = json.RawMessage(`{"org_address":"locale_or_site"}`)
	if got := orgAddressMode(cfg); got != "locale_or_site" {
		t.Errorf("orgAddressMode = %q", got)
	}
	cfg.BusinessProfile = json.RawMessage(`{"name":`)
	if got := orgAddressMode(cfg); got != "" {
		t.Errorf("orgAddressMode of a bad profile = %q, want empty", got)
	}
}

func TestLocaleBusiness_NewFields(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	cfg := businessConfig(`{"fr":{"address":{"street":"Example Street 1"},"vat_id":"V2","description":"Local","image":"/media/o.jpg","country_name":"Exampleland",
		"contact_points":[{"type":"sales","phone":"1","name":"Sales","area_served":"EU","available_language":"French"}],"rating":{"value":"3","best":"5","worst":"1","count_type":"reviews"}}}`)
	b, err := localeBusiness(cfg, set, "fr")
	if err != nil || b == nil {
		t.Fatalf("fr: %+v, %v", b, err)
	}
	if b.VATID != "V2" || b.Description != "Local" || b.Image != "/media/o.jpg" || b.CountryName != "Exampleland" {
		t.Errorf("card fields = %+v", b)
	}
	wantCP := []render.ContactPointView{{Type: "sales", Phone: "1", Name: "Sales", AreaServed: "EU", AvailableLanguage: "French"}}
	if !reflect.DeepEqual(b.ContactPoints, wantCP) {
		t.Errorf("contact points = %+v", b.ContactPoints)
	}
	if b.Rating != (render.Rating{Value: "3", Best: "5", Worst: "1", CountType: "reviews"}) {
		t.Errorf("rating = %+v", b.Rating)
	}
}

func TestLocaleNodeIDs(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	if got := localeNodeIDs(set); !reflect.DeepEqual(got, map[string]string{"en": "en", "fr": "fr"}) {
		t.Errorf("ids = %v", got)
	}
	// The default has no segment and another locale's segment equals its
	// lowercased tag: the default gets a distinct id.
	clash := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "en-XX", Segment: "en"})
	got := localeNodeIDs(clash)
	if got["en-XX"] != "en" || got["en"] != "en-default" {
		t.Errorf("colliding ids = %v", got)
	}
	cfg := businessConfig(`{"en":{"address":{"city":"A"}},"en-XX":{"address":{"city":"B"}}}`)
	a, _ := localeBusiness(cfg, clash, "en")
	b, _ := localeBusiness(cfg, clash, "en-XX")
	if a == nil || b == nil || a.ID == b.ID {
		t.Errorf("ids %+v %+v must differ", a, b)
	}
}

func TestLocaleSummaries(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"},
		entity.LocaleEntry{Tag: "nl", Segment: "nl", Hidden: true})
	cfg := businessConfig(`{"fr":{"address":{"city":"Sample City"},"phone":"fr-phone","country_name":"Exampleland","geo_region":"XX","price_range":"$$","currency":"XXD",
		"contact_points":[{"phone":"1"}]},"nl":{"phone":"hidden"}}`)
	got, err := localeSummaries(cfg, set)
	if err != nil {
		t.Fatal(err)
	}
	want := []render.LocaleSummary{
		{Tag: "en", Language: "English"},
		{Tag: "fr", Segment: "fr", CountryName: "Exampleland", Phone: "fr-phone", GeoRegion: "XX", Language: "French",
			PriceRange: "$$", Currency: "XXD", ContactPoints: []render.ContactPointView{{Phone: "1"}}, HasOffice: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summaries = %+v\nwant %+v", got, want)
	}
	one := businessSet(t, false, entity.LocaleEntry{Tag: "en"})
	if got, err := localeSummaries(cfg, one); err != nil || got != nil {
		t.Errorf("one locale: %+v, %v; want nil", got, err)
	}
	if _, err := localeSummaries(businessConfig(`[1]`), set); err == nil {
		t.Error("bad locale map: want an error")
	}
}

var ldRE = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// graphTypes renders a page through the default theme and returns the @type of
// each node of the site @graph, plus the LocalBusiness node if present.
func graphTypes(t *testing.T, b *render.BusinessView) (types []string, lb map[string]any) {
	t.Helper()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := &render.PageView{
		BaseURL: "https://example.test", Path: "/", Business: b,
		Site: render.SiteView{Name: "Example Site", Language: "en"},
	}
	var out bytes.Buffer
	if err := engine.RenderPage(&out, v); err != nil {
		t.Fatal(err)
	}
	for _, m := range ldRE.FindAllSubmatch(out.Bytes(), -1) {
		var doc struct {
			Graph []map[string]any `json:"@graph"`
		}
		if json.Unmarshal(m[1], &doc) != nil || doc.Graph == nil {
			continue
		}
		for _, n := range doc.Graph {
			types = append(types, n["@type"].(string))
			if n["@id"] == "https://example.test#localbusiness-fr" {
				lb = n
			}
		}
	}
	return types, lb
}

func TestRenderedGraph_PerLocale(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"}, entity.LocaleEntry{Tag: "nl", Segment: "nl"})
	cfg := businessConfig(cardsJSON)

	fr, _ := localeBusiness(cfg, set, "fr")
	types, lb := graphTypes(t, fr)
	if !reflect.DeepEqual(types, []string{"Organization", "Restaurant", "WebSite"}) {
		t.Fatalf("fr graph = %v", types)
	}
	parent, _ := lb["parentOrganization"].(map[string]any)
	if parent["@id"] != "https://example.test#organization" || lb["telephone"] != "site-phone" {
		t.Errorf("fr LocalBusiness = %v", lb)
	}

	for _, loc := range []string{"nl", "en"} {
		b, _ := localeBusiness(cfg, set, loc)
		if types, _ := graphTypes(t, b); !reflect.DeepEqual(types, []string{"Organization", "WebSite"}) {
			t.Errorf("%s graph = %v, want Organization and WebSite only", loc, types)
		}
	}
}

func TestLocaleGeoRegion(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"}, entity.LocaleEntry{Tag: "nl", Segment: "nl"}, entity.LocaleEntry{Tag: "it", Segment: "it"})
	cfg := businessConfig(`{"fr":{"geo_region":"ZZ-F"},"nl":{"no_office":true,"geo_region":"ZZ-N","address":{"street":"Example Street 2"}}}`)
	for loc, want := range map[string]string{"fr": "ZZ-F", "nl": "ZZ-N", "it": "", "xx": ""} {
		if got := localeGeoRegion(cfg, set, loc); got != want {
			t.Errorf("localeGeoRegion(%s) = %q, want %q", loc, got, want)
		}
	}
	one := businessSet(t, false, entity.LocaleEntry{Tag: "en"})
	if got := localeGeoRegion(cfg, one, "en"); got != "" {
		t.Errorf("one-locale site: %q, want none", got)
	}
}
