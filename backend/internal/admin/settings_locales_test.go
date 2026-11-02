package admin

import (
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
		`placeholder="555 0100"`, `placeholder="Example Co"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("General lacks %q", want)
		}
	}
	if strings.Contains(body, `<details class="hm-disclosure" id="locale-aa" open`) {
		t.Error("a card is open without an edit")
	}
}

func TestBusinessTabEmptyBelowTwoLocales(t *testing.T) {
	t.Parallel()

	for name, entries := range map[string][]siteconfig.LocaleEntry{"none": nil, "one": {{Tag: "en"}}} {
		g, _ := localeRig(t, entries...)
		body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
		if strings.Contains(body, `id="locales-card"`) || strings.Contains(body, "No office in this locale") {
			t.Errorf("%s: the Locales card shows with fewer than two locales", name)
		}
		if rec := g.do("POST", "/admin/settings/locales/business/en", "owner-token", url.Values{}, nil); rec.Code != 404 {
			t.Errorf("%s: saving a locale of a single-locale site = %d, want 404", name, rec.Code)
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
		"Site address on every page", "The locale&#39;s office; none without one", "The locale&#39;s office; site address without one",
		`name="org_address" value="" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tab lacks %q", want)
		}
	}
	if n := strings.Count(body, `name="org_address"`); n != 3 {
		t.Errorf("%d radios, want 3", n)
	}
	// Below two locales the tab is the empty state.
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

// The General business form has no org_address field; saving it keeps the
// setting, and the setting's save keeps what General stored.
func TestGeneralBusinessSaveKeepsOrgAddress(t *testing.T) {
	t.Parallel()

	g, site := localeRig(t, threeLocales...)
	site.cfg.BusinessProfile = []byte(`{"org_address":"locale_or_site"}`)
	form := url.Values{"notify": {"boss@example.com"}, "biz.tax_id": {"TX-2"}}
	if rec := g.do("POST", "/admin/settings/general/contact", "owner-token", form, nil); rec.Code != 303 {
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
	for _, want := range []string{
		"Legal name", "printed as its legal name", "Country name", "VAT ID", `placeholder="VAT-1"`, "Describes this locale&#39;s local business", "Site description", `name="loc_aa.image"`,
		"NAICS code", "Empty uses the site-wide value.",
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
		"switch": {"_present.loc_aa.accepts_reservations": {"1"}, "loc_aa.accepts_reservations": {"true"}},
		"no":     {"_present.loc_aa.accepts_reservations": {"1"}},
		"preset": {"_present.loc_aa.amenity_presets.wifi": {"1"}},
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
