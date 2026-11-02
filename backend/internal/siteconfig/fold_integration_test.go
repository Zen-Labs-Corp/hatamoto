//go:build integration

package siteconfig_test

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func seedFoldSite(t *testing.T, s *siteconfig.Store) {
	t.Helper()
	ctx := context.Background()
	phone := "555 0100"
	locales := []siteconfig.LocaleEntry{{Tag: "en-XX", Segment: "en"}, {Tag: "fr-CA", Segment: "fr"}, {Tag: "de-XX", Segment: "dx"}}
	def := "en-XX"
	if _, err := s.Update(ctx, siteconfig.UpdateInput{
		Language: &def, Phone: &phone, Locales: &locales,
		BusinessProfile: json.RawMessage(`{"tax_id":"T1"}`),
	}); err != nil {
		t.Fatal(err)
	}
	for tag, card := range map[string]string{
		"fr-CA": `{"tax_id":"T2","phone":"555 0200","address":{"city":"Sample City"}}`,
		"de-XX": `{"name":"Example DE"}`,
	} {
		if err := s.SetLocaleProfile(ctx, tag, json.RawMessage(card)); err != nil {
			t.Fatal(err)
		}
	}
}

// Changing the default locale folds the new default's card into the default
// business and copies what it overrode down to the other cards.
func TestUpdateLanguageFoldsNewDefaultCard(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	seedFoldSite(t, s)

	lang := "fr-CA"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Language: &lang})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone != "555 0200" {
		t.Errorf("Phone = %q, want the card's", cfg.Phone)
	}
	jsonEq(t, "PostalAddress", cfg.PostalAddress, `{"street":"","city":"Sample City","state":"","postal_code":"","country":""}`)
	var bp map[string]any
	if err := json.Unmarshal(cfg.BusinessProfile, &bp); err != nil {
		t.Fatal(err)
	}
	if bp["tax_id"] != "T2" || bp["office"] != true {
		t.Errorf("business_profile = %s, want tax_id T2 and office", cfg.BusinessProfile)
	}
	var cards map[string]map[string]any
	if err := json.Unmarshal(cfg.LocaleProfiles, &cards); err != nil {
		t.Fatal(err)
	}
	if _, ok := cards["fr-CA"]; ok {
		t.Errorf("new default still has a card: %s", cfg.LocaleProfiles)
	}
	if _, ok := cards["en-XX"]; ok {
		t.Errorf("old default got a card: %s", cfg.LocaleProfiles)
	}
	if cards["de-XX"]["tax_id"] != "T1" || cards["de-XX"]["phone"] != "555 0100" || cards["de-XX"]["name"] != "Example DE" {
		t.Errorf("de-XX card = %v, want copy-down of the old site values", cards["de-XX"])
	}
}

// The same language, or a language without a card, leaves the profile alone.
func TestUpdateLanguageFoldOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	seedFoldSite(t, s)
	before, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	same := "en-XX"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Language: &same})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "unchanged BusinessProfile", cfg.BusinessProfile, string(before.BusinessProfile))
	jsonEq(t, "unchanged LocaleProfiles", cfg.LocaleProfiles, string(before.LocaleProfiles))

	// An orphan card under the current language is not folded while the
	// language stays.
	if err := s.SetLocaleProfile(ctx, "en-XX", json.RawMessage(`{"tax_id":"T9"}`)); err != nil {
		t.Fatal(err)
	}
	if cfg, err = s.Update(ctx, siteconfig.UpdateInput{Language: &same}); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile with orphan", cfg.BusinessProfile, `{"tax_id":"T1"}`)

	noCard := "de-XX"
	if err := s.SetLocaleProfile(ctx, "de-XX", nil); err != nil {
		t.Fatal(err)
	}
	if cfg, err = s.Update(ctx, siteconfig.UpdateInput{Language: &noCard}); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "BusinessProfile no card", cfg.BusinessProfile, `{"tax_id":"T1"}`)
	if cfg.Phone != "555 0100" {
		t.Errorf("Phone = %q, want unchanged", cfg.Phone)
	}
}

// A profile sent with the language change is folded after it is stored, and
// SetLocaleProfile keeps working next to the transactional update.
func TestUpdateLanguageFoldsProfileSentTogether(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	seedFoldSite(t, s)
	lang := "fr-CA"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Language: &lang, BusinessProfile: json.RawMessage(`{"tax_id":"T3"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var bp map[string]any
	if err := json.Unmarshal(cfg.BusinessProfile, &bp); err != nil {
		t.Fatal(err)
	}
	if bp["tax_id"] != "T2" {
		t.Errorf("tax_id = %v, want the card's value over the stored profile", bp["tax_id"])
	}
	if err := s.SetLocaleProfile(ctx, "de-XX", json.RawMessage(`{"name":"Renamed"}`)); err != nil {
		t.Fatal(err)
	}
}
