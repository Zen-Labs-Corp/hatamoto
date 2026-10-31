//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

// termLocalesVersion is 202610312100_term_locales; Down rolls back to just
// below it.
const termLocalesVersion = 202610312100

func TestTermLocalesMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := entityLocaleDB(t)
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	// The previous release's column list still inserts.
	if err := exec(`INSERT INTO hatamoto_terms (kind, slug, name) VALUES ('category', 'guides', 'Guides')`); err != nil {
		t.Fatalf("old-column insert: %v", err)
	}
	var locale string
	var group *string
	if err := db.QueryRowContext(ctx, `SELECT locale, translation_group::text FROM hatamoto_terms WHERE slug = 'guides'`).Scan(&locale, &group); err != nil {
		t.Fatal(err)
	}
	if locale != "" || group != nil {
		t.Fatalf("defaults = %q, %v; want empty locale and no group", locale, group)
	}

	grp := "11111111-1111-1111-1111-111111111111"
	set := func(slug, loc string) error {
		return exec(`UPDATE hatamoto_terms SET locale = $2, translation_group = $3 WHERE slug = $1`, slug, loc, grp)
	}
	if err := exec(`INSERT INTO hatamoto_terms (kind, slug, name) VALUES ('category', 'guides-fr', 'Guides fr'), ('category', 'guides-fr2', 'Guides fr 2')`); err != nil {
		t.Fatal(err)
	}
	if err := set("guides", "en-US"); err != nil {
		t.Fatal(err)
	}
	if err := set("guides-fr", "fr-FR"); err != nil {
		t.Fatal(err)
	}
	if err := set("guides-fr2", "fr-FR"); err == nil || !strings.Contains(err.Error(), "23505") {
		t.Fatalf("second term of one locale in a group: %v, want a unique violation", err)
	}
	// The slug stays unique per kind across locales.
	if err := exec(`INSERT INTO hatamoto_terms (kind, slug, name, locale) VALUES ('category', 'guides', 'Dup', 'fr-FR')`); err == nil || !strings.Contains(err.Error(), "23505") {
		t.Fatalf("same kind and slug: %v, want a unique violation", err)
	}

	if _, err := provider(t, db).DownTo(ctx, termLocalesVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_terms' AND column_name IN ('locale', 'translation_group')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("columns left = %d (%v)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN ('uq_terms_group_locale', 'idx_terms_translation_group')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("indexes left = %d (%v)", n, err)
	}
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
