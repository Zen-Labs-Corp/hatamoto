//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// Both tables gain a sort_order column that defaults to 0 and an index.
func TestSortOrder_ColumnsAndIndexes(t *testing.T) {
	db, _ := multiLocaleDB(t)
	for _, c := range []struct{ table, index string }{
		{"hatamoto_entities", "idx_entities_sort_order"},
		{"hatamoto_terms", "idx_terms_sort_order"},
	} {
		var def string
		if err := db.QueryRowContext(context.Background(), `SELECT column_default FROM information_schema.columns
			WHERE table_name = $1 AND column_name = 'sort_order'`, c.table).Scan(&def); err != nil || def != "0" {
			t.Errorf("%s.sort_order default = %q (%v), want 0", c.table, def, err)
		}
		var n int
		if err := db.QueryRowContext(context.Background(),
			`SELECT count(*) FROM pg_indexes WHERE indexname = $1`, c.index).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s = %d (%v)", c.index, n, err)
		}
	}
}
