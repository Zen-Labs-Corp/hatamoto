//go:build integration

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// restProfileFixture returns the REST handlers on the real store with empty
// profiles (restored on cleanup), and helpers to send a request and to read the
// stored row.
func restProfileFixture(t *testing.T) (do func(method, body string) *httptest.ResponseRecorder, stored func() *siteconfig.Config) {
	t.Helper()
	srv, ctx := localeCheckServer(t)
	f := newSettingsSaveFixture(t, srv, ctx)
	pool := f.srv.db.Pool()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE hatamoto_site_config SET business_profile = '{}'::jsonb, locale_profiles = '{}'::jsonb`)
		f.srv.siteConfig.Invalidate()
	})
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_site_config SET business_profile = '{}'::jsonb, locale_profiles = '{}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	g := httpx.NewGroup(mux, "").Group("", injectRole("owner"))
	mountSiteSettings(g, &siteSettingsHandlers{
		store: f.srv.siteConfig, logger: f.srv.logger, invalidate: f.srv.invalidateSiteSettings,
		checkLocales: f.srv.checkLocaleSegments,
	})
	do = func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/settings/site/", strings.NewReader(body)))
		return rec
	}
	stored = func() *siteconfig.Config {
		t.Helper()
		cfg, err := f.srv.siteConfig.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	return do, stored
}

// Like the other settings saves it calls localeCheckServer itself (see
// settingsSaveFixture) because it rewrites the one shared site_config row.
//
// The REST API writes the default business and a locale card against the real
// store, deletes the card with null and leaves the stored rows untouched when
// validation fails.
func TestSettingsSave_RESTProfile_Integration(t *testing.T) {
	do, stored := restProfileFixture(t)

	body := `{"language":"en-XX","locales":[{"tag":"en-XX","segment":""},{"tag":"en-XY","segment":"yy"}],
		"business_profile":{"catalog_name":"Offers","service_provider":true,
			"services":[{"name":"Consulting","url":"/services/consulting","price_min":"100","unit_text":"HOUR"}],
			"additional_properties":[{"name":"Recommendation score","value":"9.6","min_value":"0","max_value":"10"}]},
		"locale_profiles":{"en-XY":{"catalog_name":"Plans","service_provider":false}}}`
	if rec := do(http.MethodPatch, body); rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodGet, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"catalog_name":"Plans"`) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	cfg := stored()
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		t.Fatal(err)
	}
	if site.CatalogName != "Offers" || site.ServiceProvider == nil || !*site.ServiceProvider ||
		len(site.Services) != 1 || site.Services[0].PriceMin != "100" || site.Services[0].UnitText != "HOUR" ||
		len(site.AdditionalProperties) != 1 || site.AdditionalProperties[0].MaxValue != "10" {
		t.Errorf("stored profile = %s", cfg.BusinessProfile)
	}
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		t.Fatal(err)
	}
	if card, ok := cards["en-XY"]; !ok || card.CatalogName != "Plans" || card.ServiceProvider == nil || *card.ServiceProvider {
		t.Errorf("stored cards = %s", cfg.LocaleProfiles)
	}

	// A rejected body changes nothing.
	before := string(stored().BusinessProfile) + string(stored().LocaleProfiles)
	if rec := do(http.MethodPatch, `{"business_profile":{"catalog_name":"Other","services":[{"name":"X","url":"services/x"}]}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad URL: %d %s", rec.Code, rec.Body)
	}
	if after := string(stored().BusinessProfile) + string(stored().LocaleProfiles); after != before {
		t.Errorf("rejected PATCH changed the rows:\nbefore %s\nafter  %s", before, after)
	}

	// null deletes the card; the site-wide profile stays.
	if rec := do(http.MethodPatch, `{"locale_profiles":{"en-XY":null}}`); rec.Code != http.StatusOK {
		t.Fatalf("delete card: %d %s", rec.Code, rec.Body)
	}
	cfg = stored()
	if cards, _ := bizprofile.ParseLocales(cfg.LocaleProfiles); len(cards) != 0 {
		t.Errorf("card not deleted: %s", cfg.LocaleProfiles)
	}
	if site, _ := bizprofile.Parse(cfg.BusinessProfile); site.CatalogName != "Offers" {
		t.Errorf("site-wide profile lost: %s", cfg.BusinessProfile)
	}
}

// Changing the default locale through the API folds the new default's card
// into business_profile; the default's tag then takes no card of its own, and
// the old default inherits.
func TestSettingsSave_RESTDefaultLocaleFold_Integration(t *testing.T) {
	do, stored := restProfileFixture(t)
	locales := func(def string) string {
		seg := map[string]string{"en-XX": "xx", "en-XY": "yy", "en-XZ": "zz"}
		seg[def] = ""
		return `[{"tag":"en-XX","segment":"` + seg["en-XX"] + `"},{"tag":"en-XY","segment":"` + seg["en-XY"] + `"},{"tag":"en-XZ","segment":"` + seg["en-XZ"] + `"}]`
	}
	seed := `{"language":"en-XX","locales":` + locales("en-XX") + `,
		"business_profile":{"tax_id":"T1","catalog_name":"Offers"},
		"locale_profiles":{"en-XY":{"tax_id":"T2","address":{"city":"Sample City"},"catalog_name":"Plans"},"en-XZ":{"description":"Example XZ"}}}`
	if rec := do(http.MethodPatch, seed); rec.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", rec.Code, rec.Body)
	}

	// The default tag takes no card, with the pointer to business_profile.
	rec := do(http.MethodPatch, `{"locale_profiles":{"en-XX":{"tax_id":"T9"}}}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "business_profile") {
		t.Fatalf("default tag card: %d %s", rec.Code, rec.Body)
	}

	rec = do(http.MethodPatch, `{"language":"en-XY","locales":`+locales("en-XY")+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("language change: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		BusinessProfile map[string]any            `json:"business_profile"`
		LocaleProfiles  map[string]map[string]any `json:"locale_profiles"`
	}
	if err := json.Unmarshal(do(http.MethodGet, "").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BusinessProfile["tax_id"] != "T2" || got.BusinessProfile["catalog_name"] != "Plans" || got.BusinessProfile["office"] != true {
		t.Errorf("business_profile = %v, want the new default's values and its office", got.BusinessProfile)
	}
	if _, ok := got.LocaleProfiles["en-XY"]; ok {
		t.Errorf("new default still listed in locale_profiles: %v", got.LocaleProfiles)
	}
	if _, ok := got.LocaleProfiles["en-XX"]; ok {
		t.Errorf("old default got a card: %v", got.LocaleProfiles)
	}
	if xz := got.LocaleProfiles["en-XZ"]; xz["description"] != "Example XZ" || xz["tax_id"] != "T1" || xz["catalog_name"] != "Offers" {
		t.Errorf("en-XZ = %v, want its own value and the old default's copied down", xz)
	}
	if city := stored().PostalAddress; !strings.Contains(string(city), "Sample City") {
		t.Errorf("legacy address = %s, want the new default's", city)
	}

	// The tag that is now the default is refused; the former default may take a card.
	if rec := do(http.MethodPatch, `{"locale_profiles":{"en-XY":{"tax_id":"T9"}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("new default tag card: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPatch, `{"locale_profiles":{"en-XX":{"tax_id":"T3"}}}`); rec.Code != http.StatusOK {
		t.Errorf("former default card: %d %s", rec.Code, rec.Body)
	}

	// Back to the first language: its card (written just now) folds into the business.
	if rec := do(http.MethodPatch, `{"language":"en-XX","locales":`+locales("en-XX")+`}`); rec.Code != http.StatusOK {
		t.Fatalf("language back: %d %s", rec.Code, rec.Body)
	}
	if site, _ := bizprofile.Parse(stored().BusinessProfile); site.TaxID != "T3" {
		t.Errorf("TaxID = %q after the way back, want the card's T3", site.TaxID)
	}
}
