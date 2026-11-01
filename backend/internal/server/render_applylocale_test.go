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

func TestApplyLocale_SetsSegment(t *testing.T) {
	lookup := func(*entity.LocaleSet, string) (*render.BusinessView, string) { return nil, "" }
	snapFor := func(set *entity.LocaleSet) *entity.URLSnapshot {
		reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
		reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
		snap, err := reg.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	snap := snapFor(set)
	for _, tt := range []struct{ locale, want string }{{"fr", "fr"}, {"en", ""}} {
		v := &render.PageView{}
		rr := &routeResult{Kind: resolveKindEntity, Locale: tt.locale, Page: &PublicPageResponse{Entity: &entity.Entity{Locale: tt.locale}}}
		(&Server{}).applyLocaleSnap(context.Background(), snap, rr, v, "https://example.test", lookup)
		if v.Locale != tt.locale || v.Segment != tt.want {
			t.Errorf("%s: Locale=%q Segment=%q, want segment %q", tt.locale, v.Locale, v.Segment, tt.want)
		}
	}
	// A default served without a prefix reports no segment, even when one is stored.
	storedSet, err := entity.NewLocaleSet("en-XX", []entity.LocaleEntry{{Tag: "en-XX", Segment: "xx"}, {Tag: "fr-XX", Segment: "yy"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	stored := snapFor(storedSet)
	for _, tt := range []struct{ locale, want string }{{"en-XX", ""}, {"fr-XX", "yy"}} {
		v := &render.PageView{}
		rr := &routeResult{Kind: resolveKindEntity, Locale: tt.locale, Page: &PublicPageResponse{Entity: &entity.Entity{Locale: tt.locale}}}
		(&Server{}).applyLocaleSnap(context.Background(), stored, rr, v, "https://example.test", lookup)
		if v.Segment != tt.want {
			t.Errorf("stored segment, %s: Segment=%q, want %q", tt.locale, v.Segment, tt.want)
		}
	}
	one := snapFor(businessSet(t, false, entity.LocaleEntry{Tag: "en"}))
	v := &render.PageView{}
	(&Server{}).applyLocaleSnap(context.Background(), one, &routeResult{Locale: "en"}, v, "https://example.test", lookup)
	if v.Locale != "" || v.Segment != "" {
		t.Errorf("one locale: Locale=%q Segment=%q, want both empty", v.Locale, v.Segment)
	}
	if v.Locales != nil {
		t.Errorf("one locale: Locales = %+v, want nil", v.Locales)
	}
}
