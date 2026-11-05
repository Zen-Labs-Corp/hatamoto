package server

import (
	"encoding/json/jsontext"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/testutil"
)

const (
	twoLocales  = `[{"tag":"en","segment":""},{"tag":"fr-CA","segment":"qc"}]`
	profilePath = "/settings/site/"
)

func twoLocaleStore() *localeStore {
	return &localeStore{fakeSiteStore{cfg: siteconfig.Config{
		Language: "en",
		Locales:  []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr-CA", Segment: "qc"}},
	}}}
}

type profileResp struct {
	BusinessProfile map[string]any            `json:"business_profile"`
	LocaleProfiles  map[string]map[string]any `json:"locale_profiles"`
	Error           string                    `json:"error"`
	Fields          map[string]string         `json:"fields"`
}

func decodeProfileResp(t *testing.T, body string) profileResp {
	t.Helper()
	var r profileResp
	testutil.NoError(t, jsonx.Unmarshal([]byte(body), &r))
	return r
}

func TestSiteSettings_ProfileGetEmpty(t *testing.T) {
	h := localeRouter(twoLocaleStore(), nil)
	rec := doSite(h, http.MethodGet, profilePath, "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	for _, want := range []string{`"business_profile":{}`, `"locale_profiles":{}`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET body lacks %s: %s", want, rec.Body)
		}
	}
}

func TestSiteSettings_ProfileRoundTrip(t *testing.T) {
	store := twoLocaleStore()
	h := localeRouter(store, nil)

	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"catalog_name":"Offers","service_provider":true},"locale_profiles":{"fr-CA":{"catalog_name":"Offres"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "Offers", got.BusinessProfile["catalog_name"])
	testutil.Equal(t, "Offres", got.LocaleProfiles["fr-CA"]["catalog_name"])

	// Absent keys leave both parts unchanged.
	rec = doSite(h, http.MethodPatch, profilePath, `{"name":"Example Co"}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	got = decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "Offers", got.BusinessProfile["catalog_name"])
	testutil.Equal(t, "Offres", got.LocaleProfiles["fr-CA"]["catalog_name"])

	// The site-wide profile is replaced whole.
	rec = doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"catalog_name":"Plans"}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	got = decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "Plans", got.BusinessProfile["catalog_name"])
	if _, ok := got.BusinessProfile["service_provider"]; ok {
		t.Errorf("replace kept service_provider: %v", got.BusinessProfile)
	}

	// null deletes the card; {} also.
	rec = doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":null}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.Equal(t, 0, len(decodeProfileResp(t, rec.Body.String()).LocaleProfiles))

	doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{"catalog_name":"Offres"}}}`)
	rec = doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.Equal(t, 0, len(decodeProfileResp(t, rec.Body.String()).LocaleProfiles))

	// An empty object clears the site-wide profile.
	rec = doSite(h, http.MethodPatch, profilePath, `{"business_profile":{}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.Equal(t, 0, len(decodeProfileResp(t, rec.Body.String()).BusinessProfile))
}

func TestSiteSettings_ProfileCardsKeepOthers(t *testing.T) {
	store := &localeStore{fakeSiteStore{cfg: siteconfig.Config{
		Language: "en",
		Locales:  []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr-CA", Segment: "qc"}, {Tag: "de-XX", Segment: "dx"}},
	}}}
	h := localeRouter(store, nil)
	doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{"catalog_name":"Offres"},"de-XX":{"catalog_name":"Angebote"}}}`)
	rec := doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"de-XX":{"catalog_name":"Plans"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	got := decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "Offres", got.LocaleProfiles["fr-CA"]["catalog_name"])
	testutil.Equal(t, "Plans", got.LocaleProfiles["de-XX"]["catalog_name"])
}

// The default locale's business is business_profile: a write to its tag is a
// 422 that says so, a stored leftover can be deleted, and GET never lists it.
func TestSiteSettings_ProfileDefaultTagRule(t *testing.T) {
	store := twoLocaleStore()
	h := localeRouter(store, nil)
	for _, body := range []string{
		`{"locale_profiles":{"en":{"catalog_name":"Offers"}}}`,
		`{"locale_profiles":{"en":{}}}`,
		`{"locale_profiles":{"en":null}}`,
	} {
		rec := doSite(h, http.MethodPatch, profilePath, body)
		testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, body)
		if msg := decodeProfileResp(t, rec.Body.String()).Fields["locale_profiles.en"]; !strings.Contains(msg, "business_profile") {
			t.Errorf("%s: fields = %s, want a pointer to business_profile", body, rec.Body)
		}
	}

	store.cfg.LocaleProfiles = jsontext.Value(`{"en":{"catalog_name":"Orphan"},"fr-CA":{"catalog_name":"Offres"}}`)
	rec := doSite(h, http.MethodGet, profilePath, "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	got := decodeProfileResp(t, rec.Body.String())
	if _, ok := got.LocaleProfiles["en"]; ok || got.LocaleProfiles["fr-CA"]["catalog_name"] != "Offres" {
		t.Errorf("GET locale_profiles = %v, want the other locales only", got.LocaleProfiles)
	}

	rec = doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"en":null}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "en", strings.Join(store.cardWrites, ","), "the leftover is deleted")

	// A language change moves the rule: the new default takes no card, the old one may.
	store.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en", Segment: "ee"}, {Tag: "fr-CA", Segment: "qc"}}
	rec = doSite(h, http.MethodPatch, profilePath, `{"language":"fr-CA","locale_profiles":{"fr-CA":{"catalog_name":"x"}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	rec = doSite(h, http.MethodPatch, profilePath, `{"language":"fr-CA","locale_profiles":{"en":{"catalog_name":"x"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// business_profile is the default locale's whole business: the locale-only
// fields, the office switch and the map fields are accepted; the default
// business inherits nothing, so it takes no no_inherit.
func TestSiteSettings_BusinessProfileIsTheDefaultBusiness(t *testing.T) {
	h := localeRouter(twoLocaleStore(), nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"office":true,"business_type":"Restaurant","country_name":"Exampleland","image":"/media/a.jpg","address":{"lat":"1.5","lng":"2.5","map_url":"https://example.test/map"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	bp := decodeProfileResp(t, rec.Body.String()).BusinessProfile
	for k, want := range map[string]any{"office": true, "business_type": "Restaurant", "country_name": "Exampleland", "image": "/media/a.jpg"} {
		testutil.Equal(t, want, bp[k], k)
	}
	if addr, _ := bp["address"].(map[string]any); addr["lat"] != "1.5" || addr["map_url"] != "https://example.test/map" {
		t.Errorf("address = %v", bp["address"])
	}

	rec = doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"no_inherit":["tax_id"]}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	if decodeProfileResp(t, rec.Body.String()).Fields["business_profile.no_inherit"] == "" {
		t.Errorf("no_inherit on the default business not refused: %s", rec.Body)
	}
}

// A card of another locale takes no_inherit (validated) and returns it.
func TestSiteSettings_CardNoInherit(t *testing.T) {
	h := localeRouter(twoLocaleStore(), nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{"no_inherit":["tax_id","hours_note"]}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeProfileResp(t, rec.Body.String()).LocaleProfiles["fr-CA"]["no_inherit"]
	testutil.Equal(t, "tax_id,hours_note", strings.Join(anyStrings(got), ","))
	for _, body := range []string{
		`{"locale_profiles":{"fr-CA":{"no_inherit":["name"]}}}`,
		`{"locale_profiles":{"fr-CA":{"no_inherit":["tax_id","tax_id"]}}}`,
	} {
		rec = doSite(h, http.MethodPatch, profilePath, body)
		testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, body)
		if decodeProfileResp(t, rec.Body.String()).Fields["locale_profiles.fr-CA.no_inherit"] == "" {
			t.Errorf("%s: no_inherit not keyed: %s", body, rec.Body)
		}
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.(string))
	}
	return out
}

func TestSiteSettings_ProfileLocalesAndCardInOnePatch(t *testing.T) {
	store := &localeStore{fakeSiteStore{cfg: siteconfig.Config{Language: "en"}}}
	h := localeRouter(store, nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"locales":`+twoLocales+`,"locale_profiles":{"fr-ca":{"catalog_name":"Offres"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "Offres", decodeProfileResp(t, rec.Body.String()).LocaleProfiles["fr-CA"]["catalog_name"])
}

func TestSiteSettings_ProfileValidation422Paths(t *testing.T) {
	store := twoLocaleStore()
	h := localeRouter(store, nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"services":[{"name":"SEO","url":"services/seo"}]},"locale_profiles":{"fr-CA":{"services":[{"name":"SEO","url":"//example.test"}]}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	got := decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "validation failed", got.Error)
	for _, key := range []string{"business_profile.services.0.url", "locale_profiles.fr-CA.services.0.url"} {
		if got.Fields[key] == "" {
			t.Errorf("fields lacks %s: %v", key, got.Fields)
		}
	}
}

func TestSiteSettings_ProfileUnknownKey422(t *testing.T) {
	h := localeRouter(twoLocaleStore(), nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"catalogue_name":"Offers"}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	if len(decodeProfileResp(t, rec.Body.String()).Fields) == 0 {
		t.Errorf("no fields: %s", rec.Body)
	}
	rec = doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{"catalog_name":5}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	if decodeProfileResp(t, rec.Body.String()).Fields["locale_profiles.fr-CA.catalog_name"] == "" {
		t.Errorf("wrong type not keyed by field: %s", rec.Body)
	}
}

func TestSiteSettings_ProfileUnknownTagAndSingleLocale(t *testing.T) {
	store := twoLocaleStore()
	h := localeRouter(store, nil)
	for _, body := range []string{
		`{"locale_profiles":{"es":{"catalog_name":"Ofertas"}}}`,
		`{"locale_profiles":{"es":null}}`,
		`{"locale_profiles":{"not a tag":{}}}`,
	} {
		rec := doSite(h, http.MethodPatch, profilePath, body)
		testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, body)
		if len(decodeProfileResp(t, rec.Body.String()).Fields) != 1 {
			t.Errorf("%s: fields: %s", body, rec.Body)
		}
	}
	// A patch that drops the locale list makes every card tag unknown.
	rec := doSite(h, http.MethodPatch, profilePath, `{"locales":[],"locale_profiles":{"fr-CA":{"catalog_name":"x"}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	testutil.Equal(t, 2, len(store.cfg.Locales), "locales untouched")

	single := &localeStore{fakeSiteStore{cfg: siteconfig.Config{Language: "en"}}}
	for _, body := range []string{
		`{"locale_profiles":{"en":{"catalog_name":"x"}}}`,
		`{"locale_profiles":{"fr-CA":{"catalog_name":"x"}}}`,
	} {
		rec = doSite(localeRouter(single, nil), http.MethodPatch, profilePath, body)
		testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, body)
	}
}

func TestSiteSettings_ProfileNullSiteWide400(t *testing.T) {
	h := localeRouter(twoLocaleStore(), nil)
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":null}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code)
	if !strings.Contains(rec.Body.String(), "send {} to clear") {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestSiteSettings_ProfileNonOwner403(t *testing.T) {
	for _, role := range []string{"editor", "viewer", "client"} {
		var inv int
		h := siteSettingsRouter(role, &fakeSiteStore{}, &inv)
		testutil.Equal(t, http.StatusForbidden, doSite(h, http.MethodGet, profilePath, "").Code, role)
		testutil.Equal(t, http.StatusForbidden, doSite(h, http.MethodPatch, profilePath, `{"business_profile":{}}`).Code, role)
	}
	var inv int
	h := siteSettingsRouter("", &fakeSiteStore{}, &inv)
	testutil.Equal(t, http.StatusUnauthorized, doSite(h, http.MethodPatch, profilePath, `{"business_profile":{}}`).Code)
}

func TestSiteSettings_ProfileInvalidatesOnce(t *testing.T) {
	store := twoLocaleStore()
	var inv int
	h := siteSettingsRouter("owner", &store.fakeSiteStore, &inv)
	store.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr-CA", Segment: "qc"}, {Tag: "de-XX", Segment: "dx"}}
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"catalog_name":"Offers"},"locale_profiles":{"fr-CA":{"catalog_name":"Offres"},"de-XX":{"catalog_name":"Angebote"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, 1, inv)
	testutil.Equal(t, "de-XX,fr-CA", strings.Join(store.cardWrites, ","), "cards written in sorted order")
}

func TestSiteSettings_ProfileRejectedWritesNothing(t *testing.T) {
	store := twoLocaleStore()
	var inv int
	h := siteSettingsRouter("owner", &store.fakeSiteStore, &inv)
	rec := doSite(h, http.MethodPatch, profilePath, `{"name":"Renamed","business_profile":{"catalog_name":"Offers"},"locale_profiles":{"fr-CA":{"services":[{"name":"SEO","url":"javascript:x"}]}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	testutil.Equal(t, 0, inv)
	testutil.Equal(t, 0, len(store.cardWrites))
	testutil.Equal(t, "", store.cfg.CompanyName)
	testutil.Equal(t, 0, len(store.cfg.BusinessProfile))
}

func TestSiteSettings_ProfileStaleCardCanBeDeleted(t *testing.T) {
	store := twoLocaleStore()
	store.cfg.LocaleProfiles = jsontext.Value(`{"es":{"catalog_name":"Ofertas"},"fr-CA":{"catalog_name":"Offres"}}`)
	h := localeRouter(store, nil)
	// Writing a card for a tag that is not listed is still refused.
	rec := doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"es":{"catalog_name":"x"}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	// Deleting it works, and the listed card stays.
	rec = doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"es":null}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeProfileResp(t, rec.Body.String())
	if _, ok := got.LocaleProfiles["es"]; ok {
		t.Errorf("stale card still stored: %v", got.LocaleProfiles)
	}
	testutil.Equal(t, "Offres", got.LocaleProfiles["fr-CA"]["catalog_name"])
}
