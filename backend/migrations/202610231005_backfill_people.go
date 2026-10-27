// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/person"
)

func init() {
	goose.AddMigrationContext(backfillPeople, unbackfillPeople)
}

// backfillPeople gives every author a person entity and points their entities
// at it (person-profiles). The work is person.Backfill, which is idempotent
// and also runs after importing an archive that predates people; see its doc
// for the rules. It fails with a clear message when the site already has its
// own, non-builtin `person` content type.
//
func backfillPeople(ctx context.Context, tx *sql.Tx) error { return person.Backfill(ctx, tx) }

// unbackfillPeople removes the people so the previous migration's Down can drop
// the person content type (entities cannot outlive their schema). The mirror
// authors and author_id are untouched, so bylines survive a downgrade.
func unbackfillPeople(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM hatamoto_entities WHERE schema_id IN (SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person')`)
	return err
}
