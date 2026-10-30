package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// A shared entity page and a page without an entity must never carry the
// default locale's local business, even though the site language falls back to
// the default locale.
func TestApplyLocale_SharedPagesHaveNoBusiness(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	cfg := businessConfig(`{"en":{"geo_region":"ZZ-E","address":{"street":"Example Street 1","city":"Sample City"}},"fr":{"address":{"street":"Example Street 2","city":"Sample City"}}}`)
	if b, err := localeBusiness(cfg, set, "en"); err != nil || b == nil {
		t.Fatalf("fixture: the default locale must have an office, got %+v, %v", b, err)
	}
	lookup := func(set *entity.LocaleSet, loc string) (*render.BusinessView, string) {
		b, _ := localeBusiness(cfg, set, loc)
		return b, localeGeoRegion(cfg, set, loc)
	}
	reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page := func(locale string) *routeResult {
		return &routeResult{Kind: resolveKindEntity, Locale: "en", Page: &PublicPageResponse{Entity: &entity.Entity{Locale: locale}}}
	}
	tests := []struct {
		name string
		rr   *routeResult
		want bool
	}{
		{"shared entity page", page(entity.LocaleShared), false},
		{"page without an entity", &routeResult{Locale: "en"}, true},
		{"page without a locale scope", &routeResult{Locale: entity.LocaleShared}, false},
		{"default locale entity page", page("en"), true},
	}
	for _, tt := range tests {
		v := &render.PageView{}
		(&Server{}).applyLocaleSnap(context.Background(), snap, tt.rr, v, "https://example.test", lookup)
		if (v.Business != nil) != tt.want {
			t.Errorf("%s: Business = %+v, want present = %v", tt.name, v.Business, tt.want)
		}
		if (v.GeoRegion != "") != tt.want {
			t.Errorf("%s: GeoRegion = %q, want present = %v", tt.name, v.GeoRegion, tt.want)
		}
		if v.Locale != "en" {
			t.Errorf("%s: Locale = %q, want the default locale", tt.name, v.Locale)
		}
	}
}
