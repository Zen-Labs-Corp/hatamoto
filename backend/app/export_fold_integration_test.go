//go:build integration

package app

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/portability"
)

// A default card under the old shape: it sits next to a site-wide profile it
// overrides, under a key in its non-canonical form, with a phone of its own.
const (
	oldShapeProfile = `{"name": "Example Co", "description": "Site text.", "tax_id": "TAX-0001"}`
	oldShapeCards   = `{
  "en-UK": {"name": "Example Co GB", "description": "Default text.", "phone": "+00 555 0200",
            "address": {"street": "2 Example Street", "city": "Example Town", "country": "GB"}},
  "fr-CA": {"country_name": "Otherland"}
}`
)

type siteBusiness struct{ profile, cards, phone, hours, addr string }

func readSiteBusiness(t *testing.T, p *pgxpool.Pool) siteBusiness {
	t.Helper()
	var r siteBusiness
	if err := p.QueryRow(context.Background(), `SELECT business_profile::text, locale_profiles::text,
		coalesce(phone, '<null>'), coalesce(business_hours, '<null>'), coalesce(postal_address::text, '<null>')
		FROM hatamoto_site_config`).Scan(&r.profile, &r.cards, &r.phone, &r.hours, &r.addr); err != nil {
		t.Fatal(err)
	}
	return r
}

// exportImport moves the site of src into a fresh, migrated database.
func exportImport(t *testing.T, src *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	_, dst := scratchDB(t)
	if err := migrateUp(ctx, dst, quietLogger()); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := portability.Export(ctx, src, &buf, portability.ExportOptions{MediaDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := portability.Import(ctx, dst, &buf, portability.ImportOptions{MediaDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return dst
}

// An archive from before the fold imports folded; importing the folded result
// again changes nothing.
func TestImportFoldsDefaultBusiness_Integration(t *testing.T) {
	ctx := context.Background()
	_, src := scratchDB(t)
	if err := migrateUp(ctx, src, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, src, "Fold IT", quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `UPDATE hatamoto_site_config SET language = 'en-GB', phone = '+00 555 0100',
		business_profile = $1::jsonb, locale_profiles = $2::jsonb`, oldShapeProfile, oldShapeCards); err != nil {
		t.Fatal(err)
	}

	dst := exportImport(t, src)
	got := readSiteBusiness(t, dst)
	p, err := bizprofile.Parse([]byte(got.profile))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Office || p.Name != "Example Co GB" || p.Description != "Default text." || p.TaxID != "TAX-0001" || got.phone != "+00 555 0200" {
		t.Errorf("business not folded: %+v", got)
	}
	var addr struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}
	if err := jsonx.Unmarshal([]byte(got.addr), &addr); err != nil || addr.Street != "2 Example Street" || addr.City != "Example Town" {
		t.Errorf("address = %s (%v)", got.addr, err)
	}
	cards, err := bizprofile.ParseLocales([]byte(got.cards))
	if err != nil {
		t.Fatal(err)
	}
	fr := cards["fr-CA"]
	if len(cards) != 1 || fr.Description != "Site text." || fr.CountryName != "Otherland" {
		t.Errorf("cards = %s, want only fr-CA with the site-wide description copied down", got.cards)
	}

	if again := readSiteBusiness(t, exportImport(t, dst)); again != got {
		t.Errorf("a folded archive changed on import:\n before %+v\n after  %+v", got, again)
	}
}

// A site without a default card imports exactly as it was, legacy NULLs included.
func TestImportKeepsUnfoldedBusiness_Integration(t *testing.T) {
	ctx := context.Background()
	_, src := scratchDB(t)
	if err := migrateUp(ctx, src, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, src, "Fold IT", quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `UPDATE hatamoto_site_config SET language = 'en',
		business_profile = $1::jsonb, locale_profiles = $2::jsonb`, oldShapeProfile, `{"fr-CA": {"country_name": "Otherland"}}`); err != nil {
		t.Fatal(err)
	}
	before := readSiteBusiness(t, src)
	if after := readSiteBusiness(t, exportImport(t, src)); after != before {
		t.Errorf("row changed on import:\n before %+v\n after  %+v", before, after)
	}
}
