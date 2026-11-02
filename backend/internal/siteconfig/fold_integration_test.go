//go:build integration

package siteconfig_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
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
	if cards["en-XX"]["phone"] != "555 0100" || cards["en-XX"]["no_office"] != true {
		t.Errorf("old default card = %v, want its phone and no office", cards["en-XX"])
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
	jsonEq(t, "BusinessProfile no card", cfg.BusinessProfile, `{"tax_id":"T1","address":{},"rating":{}}`)
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

// resolvedBusinesses is what every locale prints: the default business through
// ResolveDefault, the other locales as their card over it.
func resolvedBusinesses(t *testing.T, cfg *siteconfig.Config) map[string]bizprofile.Profile {
	t.Helper()
	site, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		t.Fatal(err)
	}
	site = bizprofile.FromLegacy(site, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	cards, err := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bizprofile.Profile{}
	for tag, c := range cards {
		out[tag] = bizprofile.ResolveContact(site, &c).Profile
	}
	if d := bizprofile.ResolveDefault(site); d.Office {
		out[cfg.Language] = d.Profile
	}
	return out
}

// Changing the default from en-XX to fr-CA and back keeps what both
// businesses print, and the old default keeps its own address meanwhile.
func TestUpdateLanguageRoundTripKeepsBothBusinesses(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	phone, hours := "555 0100", "Weekdays 9 to 5"
	locales := []siteconfig.LocaleEntry{{Tag: "en-XX", Segment: "en"}, {Tag: "fr-CA", Segment: "fr"}, {Tag: "de-XX", Segment: "dx"}}
	def := "en-XX"
	addr := json.RawMessage(`{"street":"1 Example St","city":"Sample City","country":"XX"}`)
	if _, err := s.Update(ctx, siteconfig.UpdateInput{
		Language: &def, Phone: &phone, BusinessHours: &hours, Locales: &locales, PostalAddress: addr,
		BusinessProfile: json.RawMessage(`{"name":"Example Legal","tax_id":"T1","email":"a@example.test","country_name":"Exampleland","office":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	for tag, card := range map[string]string{
		"fr-CA": `{"name":"Example FR","phone":"555 0200","email":"b@example.test","currency":"CAD","country_name":"Otherland","address":{"street":"2 Example St","city":"Other City"}}`,
		"de-XX": `{"no_office":true,"country_name":"Thirdland"}`,
	} {
		if err := s.SetLocaleProfile(ctx, tag, json.RawMessage(card)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvedBusinesses(t, before)

	toFR := "fr-CA"
	mid, err := s.Update(ctx, siteconfig.UpdateInput{Language: &toFR})
	if err != nil {
		t.Fatal(err)
	}
	got := resolvedBusinesses(t, mid)
	if got["en-XX"].Address.Street != "1 Example St" || got["en-XX"].Phone != "555 0100" || got["fr-CA"].Address.Street != "2 Example St" || got["fr-CA"].Phone != "555 0200" {
		t.Errorf("after en-XX to fr-CA: en-XX %+v, fr-CA %+v", got["en-XX"], got["fr-CA"])
	}
	if !reflect.DeepEqual(got["de-XX"], want["de-XX"]) {
		t.Errorf("de-XX changed: %+v, was %+v", got["de-XX"], want["de-XX"])
	}

	back, err := s.Update(ctx, siteconfig.UpdateInput{Language: &def})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolvedBusinesses(t, back); !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the printed businesses:\ngot  %+v\nwant %+v", got, want)
	}
	cards, err := bizprofile.ParseLocales(back.LocaleProfiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cards["en-XX"]; ok {
		t.Errorf("default card left after the round trip: %s", back.LocaleProfiles)
	}
}

// A new default without a card has no office and never prints the old
// default's address; the old default keeps its own.
func TestUpdateLanguageNewDefaultWithoutCardHasNoOffice(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	locales := []siteconfig.LocaleEntry{{Tag: "en-XX", Segment: "en"}, {Tag: "fr-CA", Segment: "fr"}}
	def := "en-XX"
	if _, err := s.Update(ctx, siteconfig.UpdateInput{
		Language: &def, Locales: &locales, PostalAddress: json.RawMessage(`{"street":"1 Example St","city":"Sample City"}`),
		BusinessProfile: json.RawMessage(`{"office":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	toFR := "fr-CA"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Language: &toFR})
	if err != nil {
		t.Fatal(err)
	}
	got := resolvedBusinesses(t, cfg)
	if _, ok := got["fr-CA"]; ok {
		t.Errorf("new default prints a LocalBusiness: %+v", got["fr-CA"])
	}
	if got["en-XX"].Address.Street != "1 Example St" {
		t.Errorf("old default = %+v, want its address", got["en-XX"])
	}
	if string(cfg.PostalAddress) != "" && bizprofile.FromLegacy(bizprofile.Profile{}, "", "", cfg.PostalAddress).Address.Street != "" {
		t.Errorf("postal_address still holds the old address: %s", cfg.PostalAddress)
	}
}

// On a single-language site there is no page for the old default's card: the
// business stays as it is.
func TestUpdateLanguageSingleLocaleKeepsBusiness(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	def := "en-XX"
	if _, err := s.Update(ctx, siteconfig.UpdateInput{
		Language: &def, PostalAddress: json.RawMessage(`{"street":"1 Example St","city":"Sample City"}`),
		BusinessProfile: json.RawMessage(`{"office":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	lang := "fr-CA"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Language: &lang})
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.LocaleProfiles) != "{}" {
		t.Errorf("LocaleProfiles = %s, want none", cfg.LocaleProfiles)
	}
	if bizprofile.FromLegacy(bizprofile.Profile{}, "", "", cfg.PostalAddress).Address.Street != "1 Example St" {
		t.Errorf("address lost: %s", cfg.PostalAddress)
	}
}
