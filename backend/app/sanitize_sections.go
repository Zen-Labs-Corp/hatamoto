package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
)

// sanitizeStats are the counts printed by sanitize-html-sections for one table.
type sanitizeStats struct {
	Scanned  int // rows examined
	Rows     int // rows with at least one changed html section
	Sections int // html sections whose body changed
}

// sanitizeTarget is a table column holding an attributes blob with
// page_sections or page_doc (entity attributes).
type sanitizeTarget struct {
	Label  string // printed in the report
	Table  string
	Column string
}

// sanitizeTargets are processed in order inside one transaction.
var sanitizeTargets = []sanitizeTarget{
	{"entities", "hatamoto_entities", "attributes"},
}

// runSanitizeHTMLSections rewrites page_sections[type=html].content.body of
// every entity, and the component content of page_doc, to its sanitised form
// (a changed doc counts as one section; the frozen page_sections mirror is
// cleaned in place, never created or re-derived) inside ONE
// transaction. With dryRun the transaction is rolled back, so the counts
// report what would change. Only the attributes column of changed rows is
// written; key order is preserved in the emitted JSON (jsonb itself stores
// keys in its own normalised order).
func runSanitizeHTMLSections(ctx context.Context, databaseURL string, dryRun bool, out io.Writer) error {
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	all := make([]sanitizeStats, len(sanitizeTargets))
	for i, tg := range sanitizeTargets {
		if all[i], err = sanitizeHTMLSectionsTx(ctx, tx, tg); err != nil {
			return fmt.Errorf("%s: %w", tg.Label, err)
		}
	}
	if dryRun {
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
	} else if err := tx.Commit(ctx); err != nil {
		return err
	}

	verb := "updated"
	if dryRun {
		verb = "would update"
	}
	for i, tg := range sanitizeTargets {
		st := all[i]
		fmt.Fprintf(out, "%s scanned: %d\n%s %s: %d\nhtml sections %s (%s): %d\n",
			tg.Label, st.Scanned, tg.Label, verb, st.Rows, verb, tg.Label, st.Sections)
	}
	return nil
}

func sanitizeHTMLSectionsTx(ctx context.Context, tx pgx.Tx, tg sanitizeTarget) (sanitizeStats, error) {
	var stats sanitizeStats
	type update struct {
		id    string
		attrs json.RawMessage
	}
	var updates []update

	// Table/column come from the fixed sanitizeTargets list, never user input.
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT id::text, %[1]s FROM %[2]s
		WHERE jsonb_typeof(%[1]s->'page_sections') = 'array'
			OR jsonb_typeof(%[1]s->'page_doc') = 'object' ORDER BY id`, tg.Column, tg.Table))
	if err != nil {
		return stats, err
	}
	for rows.Next() {
		var id string
		var attrs []byte
		if err := rows.Scan(&id, &attrs); err != nil {
			rows.Close()
			return stats, err
		}
		stats.Scanned++
		clean, n, err := entity.SanitizeStoredAttributes(attrs)
		if err != nil {
			rows.Close()
			return stats, fmt.Errorf("%s row %s: %w", tg.Table, id, err)
		}
		if n > 0 {
			stats.Rows++
			stats.Sections += n
			updates = append(updates, update{id, clean})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}
	for _, u := range updates {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = $2::jsonb WHERE id = $1::uuid`, tg.Table, tg.Column), u.id, string(u.attrs)); err != nil {
			return stats, fmt.Errorf("%s row %s: %w", tg.Table, u.id, err)
		}
	}
	return stats, nil
}
