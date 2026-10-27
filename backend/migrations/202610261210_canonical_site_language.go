// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/entity"
)

func init() {
	goose.AddMigrationContext(canonicalSiteLanguage, noopDown)
}

// canonicalSiteLanguage rewrites the stored site language to the form
// entity.NormalizeLocale returns (en-UK becomes en-GB, iw-IL becomes he-IL, tl
// becomes fil). Entity locales are compared to it as strings, and the older
// kill_locales migration wrote values that are not canonical. The
// site_config_language_retag trigger moves the entities from the old string to
// the new one in the same transaction and keeps their updated_at. A value that
// is not a valid tag is left as stored. Running it again changes nothing.
func canonicalSiteLanguage(ctx context.Context, tx *sql.Tx) error {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT language FROM hatamoto_site_config LIMIT 1`).Scan(&current)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	canonical, err := entity.NormalizeLocale(current)
	if err != nil || canonical == current {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE hatamoto_site_config SET language = $1 WHERE language = $2`, canonical, current)
	return err
}

// noopDown: the previous string is not recoverable and the canonical one is
// valid for every older binary.
func noopDown(context.Context, *sql.Tx) error { return nil }
