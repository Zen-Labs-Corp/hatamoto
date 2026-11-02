// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
)

func init() {
	goose.AddMigrationContext(foldDefaultBusiness, noopDown)
}

// foldDefaultBusiness moves the business details of the default locale's card
// into business_profile and the legacy phone, hours and address columns, which
// are the default locale's business. The other cards keep what they printed:
// bizprofile.Fold copies the site-wide values the default card overrode into
// them. The idempotence key is the card under the default tag: once folded it
// is gone, so a second run changes nothing. The language and the card keys are
// compared in their canonical form; a language that is not a valid tag leaves
// the row as stored.
func foldDefaultBusiness(ctx context.Context, tx *sql.Tx) error {
	var (
		language, hours, phone sql.NullString
		profile, cards, addr   []byte
	)
	err := tx.QueryRowContext(ctx, `SELECT language, business_profile, locale_profiles, phone, business_hours, postal_address
		FROM hatamoto_site_config LIMIT 1 FOR UPDATE`).Scan(&language, &profile, &cards, &phone, &hours, &addr)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	def, err := entity.NormalizeLocale(language.String)
	if err != nil {
		return nil
	}
	cards, err = normaliseCardKeys(cards)
	if err != nil {
		return err
	}
	out, changed, err := bizprofile.FoldRow(bizprofile.Row{
		Default: def, BusinessProfile: profile, LocaleProfiles: cards,
		Phone: phone.String, BusinessHours: hours.String, PostalAddress: addr,
	})
	if err != nil || !changed {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE hatamoto_site_config
		SET business_profile = $1::jsonb, locale_profiles = $2::jsonb, phone = $3, business_hours = $4, postal_address = $5::jsonb`,
		out.BusinessProfile, out.LocaleProfiles, nullIfEmpty(out.Phone, phone), nullIfEmpty(out.BusinessHours, hours), nullableJSON(out.PostalAddress))
	return err
}

// normaliseCardKeys rewrites the keys of the locale cards to their canonical
// tag; a key that is not a valid tag stays as stored.
func normaliseCardKeys(raw []byte) ([]byte, error) {
	m, err := bizprofile.ParseLocales(raw)
	if err != nil || len(m) == 0 {
		return raw, err
	}
	out := make(map[string]bizprofile.Profile, len(m))
	for k, v := range m {
		if tag, err := entity.NormalizeLocale(k); err == nil {
			k = tag
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// nullIfEmpty writes an empty value back as NULL when the column held NULL, so
// an untouched legacy column is not turned into an empty string.
func nullIfEmpty(v string, was sql.NullString) any {
	if v == "" && !was.Valid {
		return nil
	}
	return v
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
