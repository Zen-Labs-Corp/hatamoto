package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	testutil.NoError(t, json.Unmarshal([]byte(body), &r))
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
	store := twoLocaleStore()
	h := localeRouter(store, nil)
	doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"fr-CA":{"catalog_name":"Offres"},"en":{"catalog_name":"Offers"}}}`)
	rec := doSite(h, http.MethodPatch, profilePath, `{"locale_profiles":{"en":{"catalog_name":"Plans"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	got := decodeProfileResp(t, rec.Body.String())
	testutil.Equal(t, "Offres", got.LocaleProfiles["fr-CA"]["catalog_name"])
	testutil.Equal(t, "Plans", got.LocaleProfiles["en"]["catalog_name"])
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
	rec = doSite(localeRouter(single, nil), http.MethodPatch, profilePath, `{"locale_profiles":{"en":{"catalog_name":"x"}}}`)
	testutil.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
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
	store.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr-CA", Segment: "qc"}}
	rec := doSite(h, http.MethodPatch, profilePath, `{"business_profile":{"catalog_name":"Offers"},"locale_profiles":{"fr-CA":{"catalog_name":"Offres"},"en":{"catalog_name":"Offers"}}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, 1, inv)
	testutil.Equal(t, "en,fr-CA", strings.Join(store.cardWrites, ","), "cards written in sorted order")
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
	store.cfg.LocaleProfiles = json.RawMessage(`{"es":{"catalog_name":"Ofertas"},"fr-CA":{"catalog_name":"Offres"}}`)
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
