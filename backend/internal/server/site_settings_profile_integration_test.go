//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// The REST API writes the site-wide profile and a locale card against the real
// store, deletes the card with null and leaves the stored rows untouched when
// validation fails.
//
// Like the other settings saves it calls localeCheckServer itself (see
// settingsSaveFixture) because it rewrites the one shared site_config row.
func TestSettingsSave_RESTProfile_Integration(t *testing.T) {
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
	do := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/settings/site/", strings.NewReader(body)))
		return rec
	}
	stored := func() *siteconfig.Config {
		t.Helper()
		cfg, err := f.srv.siteConfig.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

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
