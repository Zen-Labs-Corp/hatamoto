//go:build integration

package siteconfig_test

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func jsonEq(t *testing.T, name string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %v (%s)", name, err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("%s = %s, want %s", name, gb, wb)
	}
}

func TestBusinessProfileDefaults(t *testing.T) {
	cfg, err := scratchStore(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile", cfg.BusinessProfile, `{}`)
	jsonEq(t, "LocaleProfiles", cfg.LocaleProfiles, `{}`)
}

// Update writes the site-wide profile next to the legacy columns, nil leaves it,
// and {} clears it. Every legacy contact column is read back unchanged.
func TestBusinessProfileUpdateRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	phone, hours, notify, company := "555 0100", "Weekdays 9 to 5", "ops@example.test", "Renamed"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{
		Phone: &phone, BusinessHours: &hours, NotificationEmail: &notify, CompanyName: &company,
		PostalAddress:   json.RawMessage(`{"street":"Example Street","city":"Sample City"}`),
		BusinessProfile: json.RawMessage(`{"tax_id":"T1","sameas":["https://example.test/a"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile", cfg.BusinessProfile, `{"tax_id":"T1","sameas":["https://example.test/a"]}`)
	jsonEq(t, "PostalAddress", cfg.PostalAddress, `{"street":"Example Street","city":"Sample City"}`)
	if cfg.Phone != phone || cfg.BusinessHours != hours || cfg.NotificationEmail != notify || cfg.CompanyName != company {
		t.Fatalf("legacy fields changed: %q %q %q %q", cfg.Phone, cfg.BusinessHours, cfg.NotificationEmail, cfg.CompanyName)
	}

	tag := "tagline"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{Tagline: &tag})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile after nil patch", cfg.BusinessProfile, `{"tax_id":"T1","sameas":["https://example.test/a"]}`)
	if cfg.Phone != phone || cfg.BusinessHours != hours {
		t.Fatalf("nil patch changed legacy fields: %q %q", cfg.Phone, cfg.BusinessHours)
	}

	cfg, err = s.Update(ctx, siteconfig.UpdateInput{BusinessProfile: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile after clear", cfg.BusinessProfile, `{}`)
	if cfg.Phone != phone {
		t.Fatalf("clearing the profile touched the phone: %q", cfg.Phone)
	}
}

func TestSetLocaleProfileMergesAndClears(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	if err := s.SetLocaleProfile(ctx, "xx-a", json.RawMessage(`{"phone":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocaleProfile(ctx, "xx-b", json.RawMessage(`{"phone":"2"}`)); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "LocaleProfiles", cfg.LocaleProfiles, `{"xx-a":{"phone":"1"},"xx-b":{"phone":"2"}}`)

	// Saving one tag again replaces only that tag.
	if err := s.SetLocaleProfile(ctx, "xx-a", json.RawMessage(`{"phone":"3"}`)); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Get(ctx)
	jsonEq(t, "LocaleProfiles after replace", cfg.LocaleProfiles, `{"xx-a":{"phone":"3"},"xx-b":{"phone":"2"}}`)

	// Empty raw removes only that tag.
	if err := s.SetLocaleProfile(ctx, "xx-a", nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Get(ctx)
	jsonEq(t, "LocaleProfiles after clear", cfg.LocaleProfiles, `{"xx-b":{"phone":"2"}}`)

	// An Update that leaves the profile fields alone keeps the locale map.
	tag := "tagline"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{Tagline: &tag})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "LocaleProfiles after Update", cfg.LocaleProfiles, `{"xx-b":{"phone":"2"}}`)
}
