package admin

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/siteconfig"
)

// localeRig is an owner rig whose site has a default locale and two more.
func localeRig(t *testing.T, entries ...siteconfig.LocaleEntry) (*ownerRig, *fakeSite) {
	t.Helper()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	site.cfg.Language = "en"
	site.cfg.Locales = entries
	return g, site
}

var threeLocales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "aa", Segment: "aa"}, {Tag: "ab", Segment: "ab"}}

func TestBusinessTabListsEveryLocaleWhenMulti(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.Phone = "555 0100"
	site.cfg.CompanyName = "Example Co"
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{
		`id="locale-en"`, `id="locale-aa"`, `id="locale-ab"`, "Afar", "Abkhazian", "No office in this locale",
		`action="/admin/settings/locales/business/aa"`, "Save Afar", `href="#locale-aa"`, "Defaults",
		"Save English", "This is also your organisation.", "This locale has an office",
		`placeholder="555 0100"`, `placeholder="Example Co"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("General lacks %q", want)
		}
	}
	if strings.Contains(body, `<details class="hm-disclosure" id="locale-aa" open`) {
		t.Error("a card is open without an edit")
	}
	// The default card comes first, is open, and is the organisation: no "no
	// office" switch, an office switch, no placeholders (it is the base).
	en, aa := strings.Index(body, `id="locale-en"`), strings.Index(body, `id="locale-aa"`)
	if en < 0 || en > aa || !strings.Contains(body, `<details class="hm-disclosure" id="locale-en" open>`) {
		t.Fatal("the default card is not first and open")
	}
	def := body[en:aa]
	if strings.Contains(def, "No office in this locale") || !strings.Contains(def, `name="loc_en.office"`) || strings.Contains(def, `placeholder="555 0100"`) {
		t.Errorf("default card: %s", def)
	}
	if strings.Contains(body[aa:], `name="loc_aa.office"`) {
		t.Error("a locale card has the office switch")
	}
}

// otherCards is the part of the Business details page after the default card
// of threeLocales: the locale cards.
func otherCards(body string) string { return body[strings.Index(body, `id="locale-aa"`):] }

// The default locale sits in the middle of the list: its card still leads.
func TestBusinessTabDefaultCardLeadsWhateverTheOrder(t *testing.T) {
	t.Parallel()

	g, _ := localeRig(t, siteconfig.LocaleEntry{Tag: "aa", Segment: "aa"}, siteconfig.LocaleEntry{Tag: "en"})
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	if strings.Index(body, `id="locale-en"`) > strings.Index(body, `id="locale-aa"`) {
		t.Error("the default card does not lead")
	}
}

// A single-language site has one card, for its language: the organisation. It
// saves, phone, hours note and address included, and no other tag is a locale.
func TestBusinessTabSingleLanguageHasOneDefaultCard(t *testing.T) {
	t.Parallel()

	for name, entries := range map[string][]siteconfig.LocaleEntry{"none": nil, "one": {{Tag: "en"}}} {
		g, site := localeRig(t, entries...)
		body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s: a11y: %v", name, issues)
		}
		for _, want := range []string{`id="locale-en"`, "Save English", "This is also your organisation.", "This locale has an office", ">Default</span>"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: tab lacks %q", name, want)
			}
		}
		if strings.Contains(body, "No office in this locale") || strings.Contains(body, "org_address") || strings.Contains(body, "Add a second locale") {
			t.Errorf("%s: the tab shows a multi-locale part", name)
		}
		form := url.Values{"loc_en.name": {"Example Co"}, "loc_en.phone": {"555 0100"}, "loc_en.hours_note": {"Mon to Fri"},
			"loc_en.address.street": {"1 Example Street"}, "loc_en.address.city": {"Exampleton"}}
		if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", form, nil); rec.Code != 303 {
			t.Fatalf("%s: save = %d %s", name, rec.Code, rec.Body.String())
		}
		if site.cfg.Phone != "555 0100" || site.cfg.BusinessHours != "Mon to Fri" || !strings.Contains(string(site.cfg.PostalAddress), "1 Example Street") {
			t.Errorf("%s: legacy columns = %q %q %s", name, site.cfg.Phone, site.cfg.BusinessHours, site.cfg.PostalAddress)
		}
		body = g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
		for _, want := range []string{`value="555 0100"`, `value="Mon to Fri"`, `value="1 Example Street"`, `value="Exampleton"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the saved card lacks %q", name, want)
			}
		}
		if rec := g.do("POST", "/admin/settings/locales/business/fr", "owner-token", url.Values{}, nil); rec.Code != 404 {
			t.Errorf("%s: saving another tag = %d, want 404", name, rec.Code)
		}
		if len(site.cfg.LocaleProfiles) > 0 && string(site.cfg.LocaleProfiles) != "{}" {
			t.Errorf("%s: a card was stored under the default tag: %s", name, site.cfg.LocaleProfiles)
		}
	}
}

func TestBusinessSaveMergesOnlyThatLocale(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.LocaleProfiles = []byte(`{"ab":{"phone":"555 0111"}}`)
	form := url.Values{
		"loc_aa.name": {"Example Branch"}, "loc_aa.business_type": {"Restaurant"},
		"loc_aa.address.street": {"1 Example Street"}, "loc_aa.address.city": {"Exampleton"},
		"loc_aa.phone": {"555 0122"},
	}
	rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/locales/business#locale-aa" {
		t.Fatalf("save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g.flushed != 1 {
		t.Errorf("render cache invalidated %d times, want 1", g.flushed)
	}
	m, err := bizprofile.ParseLocales(site.cfg.LocaleProfiles)
	if err != nil {
		t.Fatal(err)
	}
	if m["aa"].Address.City != "Exampleton" || m["aa"].BusinessType != "Restaurant" || m["aa"].Phone != "555 0122" {
		t.Errorf("aa = %+v", m["aa"])
	}
	if m["ab"].Phone != "555 0111" {
		t.Errorf("saving aa changed ab: %+v", m["ab"])
	}
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Own office") || !strings.Contains(body, `value="Exampleton"`) {
		t.Error("the saved card does not read back")
	}
	// Clearing every field removes the locale's entry.
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{
		"loc_aa.name": {""}, "loc_aa.business_type": {""}, "loc_aa.address.street": {""}, "loc_aa.address.city": {""}, "loc_aa.phone": {""},
	}, nil); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	m, _ = bizprofile.ParseLocales(site.cfg.LocaleProfiles)
	if _, ok := m["aa"]; ok || m["ab"].Phone != "555 0111" {
		t.Errorf("after clearing: %v", m)
	}
}

func TestBusinessSaveUnknownTagIs404(t *testing.T) {
	t.Parallel()

	g, _ := localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/business/xx", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("unknown tag = %d, want 404", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
}

func TestBusinessSaveInvalidKeepsValuesAndOpensOnlyThatCard(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{
		"loc_aa.address.city": {"Keepville"}, "loc_aa.address.lat": {"999"},
	}, nil)
	if rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="locale-aa" open`, `value="Keepville"`, `href="#sf-loc_aa-address-lat"`, "Enter a number between -90 and 90."} {
		if !strings.Contains(body, want) {
			t.Errorf("422 page lacks %q", want)
		}
	}
	if strings.Contains(body, `id="locale-ab" open`) {
		t.Error("another card opened")
	}
	if g.flushed != 0 || len(site.cfg.LocaleProfiles) != 0 {
		t.Error("a failed save stored or invalidated")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestBusinessAddRowReRendersOneCardWithoutSaving(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.LocaleProfiles = []byte(`{"ab":{"phone":"555 0111"}}`)
	rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{
		"loc_aa.phone": {"555 0122"}, "_present.loc_aa.opening_hours": {"1"}, "_add.loc_aa.opening_hours": {"1"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("add row: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="locale-aa" open`) || !strings.Contains(body, `value="555 0122"`) || !strings.Contains(body, "<details open>") {
		t.Error("the edited card lost its values or did not open the new row")
	}
	if strings.Contains(body, `id="locale-ab" open`) || !strings.Contains(body, `value="555 0111"`) {
		t.Error("another card changed")
	}
	if g.flushed != 0 || string(site.cfg.LocaleProfiles) != `{"ab":{"phone":"555 0111"}}` {
		t.Error("adding a row saved the card")
	}
}

func TestBusinessTabOrgAddressDefaultsToSiteAddress(t *testing.T) {
	t.Parallel()

	g, _ := localeRig(t, threeLocales...)
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`action="/admin/settings/locales/organisation"`, "Organisation address on locale pages",
		"The default locale&#39;s address on every page", "The locale&#39;s office; none without one", "The locale&#39;s office; the default locale&#39;s address without one",
		`name="org_address" value="" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tab lacks %q", want)
		}
	}
	if n := strings.Count(body, `name="org_address"`); n != 3 {
		t.Errorf("%d radios, want 3", n)
	}
	// Below two locales there are no locale pages to choose for.
	one, _ := localeRig(t, siteconfig.LocaleEntry{Tag: "en"})
	if strings.Contains(one.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String(), "org_address") {
		t.Error("the setting shows with one locale")
	}
}

func TestOrgAddressSaveStoresEachChoiceAndKeepsTheProfile(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.Phone = "555 0100"
	site.cfg.BusinessProfile = []byte(`{"tax_id":"TX-1","contact_points":[{"type":"sales","phone":"555 0111"}],"opening_hours":[{"days":["Monday"],"opens":"09:00","closes":"17:00"}]}`)
	for _, c := range []struct{ value, want string }{
		{bizprofile.OrgAddressLocale, `"org_address":"locale"`},
		{bizprofile.OrgAddressLocaleOrSite, `"org_address":"locale_or_site"`},
		{"", ""},
	} {
		rec := g.do("POST", "/admin/settings/locales/organisation", "owner-token", url.Values{"org_address": {c.value}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/locales/business#organisation-address" {
			t.Fatalf("%q: %d %s", c.value, rec.Code, rec.Header().Get("Location"))
		}
		raw := string(site.cfg.BusinessProfile)
		if c.want != "" && !strings.Contains(raw, c.want) || c.want == "" && strings.Contains(raw, "org_address") {
			t.Errorf("%q: profile = %s", c.value, raw)
		}
		for _, keep := range []string{`"tax_id":"TX-1"`, `"phone":"555 0111"`, `"opens":"09:00"`} {
			if !strings.Contains(raw, keep) {
				t.Errorf("%q: profile lost %s: %s", c.value, keep, raw)
			}
		}
		if strings.Contains(raw, "555 0100") || site.cfg.Phone != "555 0100" {
			t.Errorf("%q: legacy phone leaked into the profile or changed: %s / %q", c.value, raw, site.cfg.Phone)
		}
		body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
		if want := `name="org_address" value="` + c.value + `" checked`; !strings.Contains(body, want) {
			t.Errorf("%q: the tab does not read it back", c.value)
		}
	}
	if g.flushed != 3 {
		t.Errorf("render cache invalidated %d times, want 3", g.flushed)
	}
}

func TestOrgAddressSaveRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"tax_id":"TX-1"}`)
	rec := g.do("POST", "/admin/settings/locales/organisation", "owner-token", url.Values{"org_address": {"always"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose one of the options.") {
		t.Fatalf("code = %d", rec.Code)
	}
	if string(site.cfg.BusinessProfile) != `{"tax_id":"TX-1"}` || g.flushed != 0 {
		t.Errorf("an invalid save changed the profile: %s", site.cfg.BusinessProfile)
	}
}

func TestOrgAddressSaveRefusesAnUnreadableProfile(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	const stored = `{"tax_id":"TX-1","employees_min":"ten"}`
	site.cfg.BusinessProfile = []byte(stored)
	rec := g.do("POST", "/admin/settings/locales/organisation", "owner-token", url.Values{"org_address": {"locale"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "nothing was saved") {
		t.Fatalf("code = %d", rec.Code)
	}
	if string(site.cfg.BusinessProfile) != stored || g.flushed != 0 {
		t.Errorf("an unreadable profile was overwritten: %s", site.cfg.BusinessProfile)
	}
}

func TestOrgAddressSaveIsOwnerOnly(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/organisation", "editor-token", url.Values{"org_address": {"locale"}}, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	if len(site.cfg.BusinessProfile) != 0 {
		t.Errorf("an editor changed the profile: %s", site.cfg.BusinessProfile)
	}
}

// The default card has no org_address field; saving it keeps the setting, and
// the setting's save keeps what the card stored.
func TestGeneralBusinessSaveKeepsOrgAddress(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"org_address":"locale_or_site"}`)
	form := url.Values{"loc_en.tax_id": {"TX-2"}}
	if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	raw := string(site.cfg.BusinessProfile)
	if !strings.Contains(raw, `"org_address":"locale_or_site"`) || !strings.Contains(raw, `"tax_id":"TX-2"`) {
		t.Errorf("profile = %s", raw)
	}
}

func TestLocaleCardHelpAndFallbacks(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"vat_id":"VAT-1","description":"Site description"}`)
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	body = otherCards(body)
	for _, want := range []string{
		"Legal name", "printed as its legal name", "Country name", "VAT ID", `placeholder="VAT-1"`, "Describes this locale&#39;s local business", "Site description", `name="loc_aa.image"`,
		"NAICS code", "Empty uses the default locale&#39;s value.", "Kilometres around this locale&#39;s coordinates.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	for _, gone := range []string{"Business name", "Alternate name", "Slogan", "Employees from"} {
		if strings.Contains(body, gone) {
			t.Errorf("card shows %q", gone)
		}
	}
}

func TestLocaleCardTriStateKeepsNotStated(t *testing.T) {
	t.Parallel()

	g, _ := localeRig(t, threeLocales...)
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	body = otherCards(body)
	for _, want := range []string{`<option value="">Not stated</option>`, "Not stated uses the default locale&#39;s value."} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	for _, gone := range []string{"Select Accepts reservations", "Choose Not stated to say nothing"} {
		if strings.Contains(body, gone) {
			t.Errorf("card shows %q", gone)
		}
	}
}

func TestLocaleCardFallsBackToSiteIdentifiersAndLinks(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"naics":"541511","isic":"6201","duns":"123456789","lei":"5493001KJTIIGC8Y1R12","gln":"1234567890123",` +
		`"booking_url":"https://example.test/book","order_url":"https://example.test/order","menu_url":"https://example.test/menu"}`)
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`placeholder="541511"`, `placeholder="6201"`, `placeholder="123456789"`, `placeholder="5493001KJTIIGC8Y1R12"`,
		`placeholder="1234567890123"`, `placeholder="https://example.test/book"`, `placeholder="https://example.test/order"`, `placeholder="https://example.test/menu"`} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
}

func TestBusinessSaveStoresACardWithOnlyAnExtendedField(t *testing.T) {
	t.Parallel()

	for name, form := range map[string]url.Values{
		"text":   {"loc_aa.naics": {"541511"}},
		"yes":    {"loc_aa.accepts_reservations": {"yes"}},
		"no":     {"loc_aa.accepts_reservations": {"no"}},
		"preset": {"loc_aa.amenity_presets.wifi": {"no"}},
	} {
		g, site := localeRig(t, threeLocales...)
		if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", form, nil); rec.Code != 303 {
			t.Fatalf("%s: save: %d", name, rec.Code)
		}
		m, err := bizprofile.ParseLocales(site.cfg.LocaleProfiles)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m["aa"]; !ok {
			t.Errorf("%s: a card with only an extended field was dropped as empty", name)
		}
	}
	// Untouched switches and empty fields leave nothing to store.
	g, site := localeRig(t, threeLocales...)
	site.cfg.LocaleProfiles = []byte(`{"aa":{"naics":"541511"}}`)
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{"loc_aa.naics": {""}, "loc_aa.booking_url": {""}}, nil); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	m, _ := bizprofile.ParseLocales(site.cfg.LocaleProfiles)
	if _, ok := m["aa"]; ok {
		t.Errorf("a card with zero values was kept: %+v", m["aa"])
	}
}

// A LEI is stored in capitals, however it was typed, on both forms.
func TestBusinessSaveUppercasesLEI(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", url.Values{"loc_en.lei": {"5493001kjtiigc8y1r12"}}, nil); rec.Code != 303 {
		t.Fatalf("site save: %d %s", rec.Code, rec.Body.String())
	}
	if raw := string(site.cfg.BusinessProfile); !strings.Contains(raw, `"lei":"5493001KJTIIGC8Y1R12"`) {
		t.Errorf("site profile = %s", raw)
	}
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{"loc_aa.lei": {"5493001kjtiigc8y1r12"}}, nil); rec.Code != 303 {
		t.Fatalf("card save: %d", rec.Code)
	}
	if raw := string(site.cfg.LocaleProfiles); !strings.Contains(raw, `"lei":"5493001KJTIIGC8Y1R12"`) {
		t.Errorf("card profile = %s", raw)
	}
}

func TestDefaultBusinessSaveKeepsGapFields(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	form := gapForm("loc_en.", "yes")
	if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got, err := bizprofile.Parse(site.cfg.BusinessProfile)
	if err != nil {
		t.Fatal(err)
	}
	if got.CatalogName != "Marketing Services" || got.ServiceProvider == nil || !*got.ServiceProvider ||
		len(got.Services) != 1 || got.Services[0].PriceMax != "50000" || got.Services[0].URL != "/services/seo" ||
		len(got.AdditionalProperties) != 1 || got.AdditionalProperties[0].MaxValue != "10" {
		t.Errorf("stored profile = %s", site.cfg.BusinessProfile)
	}
	// An empty site stays free of the new keys.
	g, site = localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", url.Values{"loc_en.name": {"Example Co"}}, nil); rec.Code != 303 {
		t.Fatalf("plain save: %d", rec.Code)
	}
	for _, k := range []string{"catalog_name", "service_provider", "additional_properties", "price_min"} {
		if strings.Contains(string(site.cfg.BusinessProfile), k) {
			t.Errorf("an untouched form stored %q: %s", k, site.cfg.BusinessProfile)
		}
	}
}

func TestBusinessSaveCardGapFields(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", gapForm("loc_aa.", "no"), nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	m, err := bizprofile.ParseLocales(site.cfg.LocaleProfiles)
	if err != nil {
		t.Fatal(err)
	}
	c := m["aa"]
	if c.CatalogName != "Marketing Services" || c.ServiceProvider == nil || *c.ServiceProvider ||
		len(c.Services) != 1 || c.Services[0].UnitText != "MONTH" || len(c.AdditionalProperties) != 1 || c.AdditionalProperties[0].Name != "Recommendation score" {
		t.Errorf("card = %s", site.cfg.LocaleProfiles)
	}
	// A card with only the provider answer is kept, not dropped as empty.
	g, site = localeRig(t, threeLocales...)
	if rec := g.do("POST", "/admin/settings/locales/business/ab", "owner-token", url.Values{"loc_ab.service_provider": {"no"}}, nil); rec.Code != 303 {
		t.Fatalf("provider only: %d", rec.Code)
	}
	m, _ = bizprofile.ParseLocales(site.cfg.LocaleProfiles)
	if _, ok := m["ab"]; !ok {
		t.Error("a card with only service_provider=no was dropped")
	}
}

func TestLocaleCardGapHelpAndPlaceholders(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"catalog_name":"Marketing Services"}`)
	site.cfg.LocaleProfiles = []byte(`{"aa":{"services":[{"name":"SEO"}],"additional_properties":[{"name":"Score","value":"1"}]}}`)
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	body = otherCards(body)
	for _, want := range []string{`placeholder="Marketing Services"`, "Extra facts printed on the organisation", "Empty uses the default locale&#39;s value.",
		"Not stated uses the default locale&#39;s value.", "Price from", "Lowest", "on a locale page it opens under that locale."} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	if strings.Contains(body, "Choose Not stated to say nothing") {
		t.Error("card shows the site-wide tri-state help")
	}
}

// The default card stores phone, hours note and address in their legacy columns
// and blanks them in the profile JSON, and keeps what the form has no control
// for (the organisation address choice) and what the old General form dropped
// (coordinates, map address).
func TestDefaultBusinessSaveWritesLegacyColumns(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"org_address":"locale","address":{"lat":"-33.5","lng":"151.2","map_url":"https://maps.example/x"}}`)
	form := url.Values{"loc_en.name": {"Example Co"}, "loc_en.phone": {"555 0100"}, "loc_en.hours_note": {"Mon to Fri"}, "loc_en.tax_id": {"TX-1"},
		"loc_en.address.street": {"1 Example Street"}, "loc_en.address.city": {"Exampleton"}, "loc_en.address.postal_code": {"1000"}, "loc_en.address.country": {"AU"},
		"loc_en.address.lat": {"-33.5"}, "loc_en.address.lng": {"151.2"}, "loc_en.address.map_url": {"https://maps.example/x"}, "_present.loc_en.office": {"1"}, "loc_en.office": {"true"}}
	rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/locales/business#locale-en" {
		t.Fatalf("save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if site.cfg.Phone != "555 0100" || site.cfg.BusinessHours != "Mon to Fri" {
		t.Errorf("columns = %q %q", site.cfg.Phone, site.cfg.BusinessHours)
	}
	var addr map[string]string
	if err := json.Unmarshal(site.cfg.PostalAddress, &addr); err != nil || addr["street"] != "1 Example Street" || addr["city"] != "Exampleton" || addr["postal_code"] != "1000" || addr["country"] != "AU" {
		t.Errorf("postal address = %s", site.cfg.PostalAddress)
	}
	var stored map[string]any
	if err := json.Unmarshal(site.cfg.BusinessProfile, &stored); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"phone", "hours_note"} {
		if _, ok := stored[k]; ok {
			t.Errorf("the profile JSON still holds %q: %s", k, site.cfg.BusinessProfile)
		}
	}
	a, _ := stored["address"].(map[string]any)
	for _, k := range []string{"street", "city", "postal_code", "country"} {
		if _, ok := a[k]; ok {
			t.Errorf("the profile JSON still holds address.%s: %s", k, site.cfg.BusinessProfile)
		}
	}
	if stored["org_address"] != "locale" || stored["tax_id"] != "TX-1" || stored["office"] != true || a["lat"] != "-33.5" || a["lng"] != "151.2" || a["map_url"] != "https://maps.example/x" {
		t.Errorf("profile = %s", site.cfg.BusinessProfile)
	}
	if len(site.cfg.LocaleProfiles) > 0 && string(site.cfg.LocaleProfiles) != "{}" {
		t.Errorf("a card was stored under the default tag: %s", site.cfg.LocaleProfiles)
	}
	// Switching the office off again is stored too.
	form.Del("loc_en.office")
	if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("second save: %d", rec.Code)
	}
	if strings.Contains(string(site.cfg.BusinessProfile), `"office"`) {
		t.Errorf("office stayed on: %s", site.cfg.BusinessProfile)
	}
}

// A locale card save leaves the default business, and the other cards, alone.
func TestLocaleCardSaveLeavesTheDefaultBusiness(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.Phone = "555 0100"
	site.cfg.BusinessProfile = []byte(`{"tax_id":"TX-1","office":true}`)
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{"loc_aa.phone": {"555 0122"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if site.cfg.Phone != "555 0100" || string(site.cfg.BusinessProfile) != `{"tax_id":"TX-1","office":true}` {
		t.Errorf("default business changed: %q %s", site.cfg.Phone, site.cfg.BusinessProfile)
	}
}

// An invalid default card is refused with the card's values kept.
func TestDefaultBusinessSaveRejectsInvalid(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", url.Values{"loc_en.phone": {"555 0100"}, "loc_en.email": {"not an email"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `value="555 0100"`) {
		t.Fatalf("save: %d", rec.Code)
	}
	if site.cfg.Phone != "" {
		t.Error("an invalid save stored the phone")
	}
}

// A field a card does not inherit shows as "Not inherited, empty" with a way
// to inherit again; ticking it, or saving a value, drops the marker.
func TestLocaleCardNotInheritedField(t *testing.T) {
	t.Parallel()

	setup := func() (*ownerRig, *fakeSite) {
		g, site := localeRig(t, threeLocales...)
		site.cfg.BusinessProfile = []byte(`{"hours_note":"Mon to Fri","tax_id":"TX-1"}`)
		site.cfg.LocaleProfiles = []byte(`{"aa":{"hours_note":"","phone":"555 0122","no_inherit":["hours_note","amenity_presets.wifi"]}}`)
		return g, site
	}
	g, _ := setup()
	body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
	aa := otherCards(body)
	aa = aa[:strings.Index(aa, `id="locale-ab"`)]
	for _, want := range []string{`placeholder="Not inherited, empty"`, "Not inherited, empty.", `name="loc_aa.inherit" value="hours_note"`, `name="loc_aa.inherit" value="amenity_presets.wifi"`, "Hours note: use the default locale&#39;s value"} {
		if !strings.Contains(aa, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	if strings.Contains(aa, `placeholder="Mon to Fri"`) {
		t.Error("the not-inherited field still shows the default locale's value")
	}
	if strings.Contains(body[:strings.Index(body, `id="locale-aa"`)], "loc_en.inherit") {
		t.Error("the default card offers to inherit")
	}

	stored := func(site *fakeSite) bizprofile.Profile {
		m, err := bizprofile.ParseLocales(site.cfg.LocaleProfiles)
		if err != nil {
			t.Fatal(err)
		}
		return m["aa"]
	}
	// Saving untouched keeps both markers.
	g, site := setup()
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{"loc_aa.phone": {"555 0122"}}, nil); rec.Code != 303 {
		t.Fatalf("plain save: %d", rec.Code)
	}
	if got := stored(site).NoInherit; len(got) != 2 {
		t.Errorf("no_inherit = %v, want both kept", got)
	}
	// Ticking inherit again clears that key only.
	g, site = setup()
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", url.Values{"loc_aa.phone": {"555 0122"}, "loc_aa.inherit": {"hours_note"}}, nil); rec.Code != 303 {
		t.Fatalf("inherit save: %d", rec.Code)
	}
	if got := stored(site).NoInherit; len(got) != 1 || got[0] != "amenity_presets.wifi" {
		t.Errorf("no_inherit = %v, want only the wifi key", got)
	}
	// A value of its own replaces the marker, a tri-state answer included.
	g, site = setup()
	form := url.Values{"loc_aa.hours_note": {"Daily"}, "loc_aa.amenity_presets.wifi": {"no"}}
	if rec := g.do("POST", "/admin/settings/locales/business/aa", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("value save: %d", rec.Code)
	}
	if got := stored(site); len(got.NoInherit) != 0 || got.HoursNote != "Daily" {
		t.Errorf("card = %s", site.cfg.LocaleProfiles)
	}
	// Inheriting the last key leaves no no_inherit key behind.
	if strings.Contains(string(site.cfg.LocaleProfiles), "no_inherit") {
		t.Errorf("stored card = %s", site.cfg.LocaleProfiles)
	}
}
