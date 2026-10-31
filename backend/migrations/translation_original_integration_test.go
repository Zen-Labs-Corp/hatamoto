//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

// translationOriginalVersion is 202610311210_translation_original; Down rolls
// back to just below it.
const translationOriginalVersion = 202610311210

func TestTranslationOriginalMigration(t *testing.T) {
	ctx := context.Background()
	db, flat := entityLocaleDB(t)
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	grp1, grp2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, r := range []struct{ slug, loc, grp string }{
		{"a", "en", grp1}, {"b", "fr", grp1}, {"c", "en", grp2}, {"d", "de", ""}, {"e", "es", ""},
	} {
		var grp any
		if r.grp != "" {
			grp = r.grp
		}
		if err := exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, locale, translation_group)
			VALUES ($1, $2, $2, $3, $4)`, flat, r.slug, r.loc, grp); err != nil {
			t.Fatal(err)
		}
	}
	var flagged int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_entities WHERE translation_original`).Scan(&flagged); err != nil || flagged != 0 {
		t.Fatalf("flagged by default = %d (%v), want 0", flagged, err)
	}
	flag := func(slug string) error {
		return exec(`UPDATE hatamoto_entities SET translation_original = true WHERE slug = $1`, slug)
	}
	for _, s := range []string{"a", "c", "d", "e"} {
		if err := flag(s); err != nil {
			t.Fatalf("flag %s: %v", s, err)
		}
	}
	err := flag("b")
	if err == nil || !strings.Contains(err.Error(), "23505") {
		t.Fatalf("second flagged row in one group: %v, want a unique violation", err)
	}

	if _, err := provider(t, db).DownTo(ctx, translationOriginalVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_entities' AND column_name = 'translation_original'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("column left = %d (%v)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'uq_entities_translation_original'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("index left = %d (%v)", n, err)
	}
}
