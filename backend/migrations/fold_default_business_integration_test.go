//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/bizprofile"
)

// foldDefaultBusinessVersion is 202611021100_fold_default_business.
const foldDefaultBusinessVersion = 202611021100

type businessRow struct{ profile, cards, phone, hours, addr string }

// foldDB is a scratch database migrated to just below the fold, holding one
// site config row with the given language and old-shape business data.
func foldDB(t *testing.T, language, profile, cards, phone, hours, addr string) *sql.DB {
	t.Helper()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).UpTo(context.Background(), foldDefaultBusinessVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO hatamoto_site_config (company_name, language, business_profile, locale_profiles, phone, business_hours, postal_address)
		VALUES ('Example', $1, $2::jsonb, $3::jsonb, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, '')::jsonb)`,
		language, profile, cards, phone, hours, addr); err != nil {
		t.Fatal(err)
	}
	return db
}

func foldUp(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := provider(t, db).Up(context.Background()); err != nil {
		t.Fatalf("up: %v", err)
	}
}

func readBusiness(t *testing.T, db *sql.DB) businessRow {
	t.Helper()
	var r businessRow
	if err := db.QueryRow(`SELECT business_profile::text, locale_profiles::text, COALESCE(phone, '<null>'),
		COALESCE(business_hours, '<null>'), COALESCE(postal_address::text, '<null>') FROM hatamoto_site_config`).
		Scan(&r.profile, &r.cards, &r.phone, &r.hours, &r.addr); err != nil {
		t.Fatal(err)
	}
	return r
}

func profileOf(t *testing.T, raw string) bizprofile.Profile {
	t.Helper()
	p, err := bizprofile.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cardsOf(t *testing.T, raw string) map[string]bizprofile.Profile {
	t.Helper()
	m, err := bizprofile.ParseLocales([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The migration is re-run through Down (a no-op) and Up: the second run must
// leave the folded row as it was.
func assertFoldIdempotent(t *testing.T, db *sql.DB) {
	t.Helper()
	before := readBusiness(t, db)
	p := provider(t, db)
	if _, err := p.DownTo(context.Background(), foldDefaultBusinessVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(context.Background()); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if after := readBusiness(t, db); after != before {
		t.Errorf("second run changed the row:\n before %+v\n after  %+v", before, after)
	}
}

// No card under the default tag: the row is not touched, legacy NULLs included.
func TestFoldDefaultBusiness_NoDefaultCard(t *testing.T) {
	cards := `{"fr-CA": {"phone": "+00 555 0300"}}`
	db := foldDB(t, "en-XX", `{"name": "Example Co"}`, cards, "", "", "")
	before := readBusiness(t, db)
	foldUp(t, db)
	if after := readBusiness(t, db); after != before {
		t.Errorf("row changed:\n before %+v\n after  %+v", before, after)
	}
	assertFoldIdempotent(t, db)
}

// A default card that equals the site values folds to the same business, with
// no office.
func TestFoldDefaultBusiness_EqualCard(t *testing.T) {
	db := foldDB(t, "en-XX", `{"description": "Makes examples."}`,
		`{"en-XX": {"description": "Makes examples."}}`, "+00 555 0100", "Mon-Fri 9-5", "")
	foldUp(t, db)
	r := readBusiness(t, db)
	if len(cardsOf(t, r.cards)) != 0 {
		t.Errorf("cards = %s, want none", r.cards)
	}
	p := profileOf(t, r.profile)
	if p.Description != "Makes examples." || p.Office || r.phone != "+00 555 0100" || r.hours != "Mon-Fri 9-5" {
		t.Errorf("row = %+v", r)
	}
	assertFoldIdempotent(t, db)
}

// A default card with an office and different values wins; the other card
// keeps the site-wide value it used to inherit.
func TestFoldDefaultBusiness_OfficeAndDifferingValues(t *testing.T) {
	profile := `{"name": "Example Co", "description": "Site text.", "tax_id": "TAX-0001"}`
	cards := `{
		"en-XX": {"name": "Example Co XX", "description": "Default text.", "phone": "+00 555 0200",
			"address": {"street": "2 Example Street", "city": "Example Town", "country": "XX"}},
		"fr-CA": {"country_name": "Otherland"}
	}`
	db := foldDB(t, "en-XX", profile, cards, "+00 555 0100", "", `{"street": "1 Site Street", "city": "Site City", "country": "XX"}`)
	foldUp(t, db)
	r := readBusiness(t, db)
	p := profileOf(t, r.profile)
	if !p.Office || p.Name != "Example Co XX" || p.Description != "Default text." || p.TaxID != "TAX-0001" {
		t.Errorf("business = %+v", p)
	}
	if r.phone != "+00 555 0200" {
		t.Errorf("phone = %q, want the default card's", r.phone)
	}
	var addr struct{ Street, City string }
	if err := json.Unmarshal([]byte(r.addr), &addr); err != nil || addr.Street != "2 Example Street" || addr.City != "Example Town" {
		t.Errorf("address = %s (%v)", r.addr, err)
	}
	got := cardsOf(t, r.cards)
	if _, ok := got["en-XX"]; ok || len(got) != 1 {
		t.Fatalf("cards = %s, want only fr-CA", r.cards)
	}
	fr := got["fr-CA"]
	if fr.Description != "Site text." || fr.Phone != "+00 555 0100" || fr.CountryName != "Otherland" || fr.Name != "" || fr.Address.Street != "" {
		t.Errorf("fr-CA = %+v, want the site-wide description and phone copied down and nothing else inherited", fr)
	}
	assertFoldIdempotent(t, db)
}

// A card key stored in a non-canonical form (en-UK) is the default card of the
// canonical language (en-GB).
func TestFoldDefaultBusiness_LegacyTag(t *testing.T) {
	db := foldDB(t, "en-GB", `{}`, `{"en-UK": {"description": "Default text.", "address": {"city": "Example Town"}}}`, "", "", "")
	foldUp(t, db)
	r := readBusiness(t, db)
	p := profileOf(t, r.profile)
	if p.Description != "Default text." || !p.Office || len(cardsOf(t, r.cards)) != 0 {
		t.Errorf("row = %+v", r)
	}
	assertFoldIdempotent(t, db)
}

// An invalid language leaves the row as stored.
func TestFoldDefaultBusiness_InvalidLanguage(t *testing.T) {
	db := foldDB(t, "not a tag", `{}`, `{"en-XX": {"description": "x"}}`, "", "", "")
	before := readBusiness(t, db)
	foldUp(t, db)
	if after := readBusiness(t, db); after != before {
		t.Errorf("row changed:\n before %+v\n after  %+v", before, after)
	}
}

// A fresh database has no site config row yet: nothing to fold, no error.
func TestFoldDefaultBusiness_NoRow(t *testing.T) {
	db, _ := scratchDB(t)
	foldUp(t, db)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM hatamoto_site_config`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows = %d (%v)", n, err)
	}
}
