//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

const (
	neutralLeadIndexesVersion = 202610261000
	// A stand-in for the segment older installs carry in the four names.
	oldSegment = "legacy"
)

var leadIndexSuffixes = []string{"intent_score", "intent_tier", "merged_into", "sales_engaged_at"}

// leadIndexes lists "name: definition" for every index in schema public, sorted.
func leadIndexes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT indexname || ': ' || indexdef FROM pg_indexes WHERE schemaname = 'public' ORDER BY indexname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// segmentedLeadIndexes lists the indexes of schema public whose name has a segment
// between idx_ and _leads_.
func segmentedLeadIndexes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT indexname FROM pg_indexes WHERE indexname ~ '^idx_.+_leads_' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The baseline creates the neutral names (hatamoto_leads is dropped again by
// 017, so look right after 016), and a full migrate leaves no segmented name.
func TestNeutralLeadIndexNames_FreshInstall(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, 16); err != nil {
		t.Fatalf("up to 16: %v", err)
	}
	for _, s := range leadIndexSuffixes {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'hatamoto_leads' AND indexname = $1`,
			"idx_leads_"+s).Scan(&n); err != nil || n != 1 {
			t.Errorf("idx_leads_%s count = %d (%v), want 1", s, n, err)
		}
	}
	if odd := segmentedLeadIndexes(t, db); len(odd) > 0 {
		t.Errorf("index names with a segment before _leads_ after 016: %v", odd)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if odd := segmentedLeadIndexes(t, db); len(odd) > 0 {
		t.Errorf("index names with a segment before _leads_ at head: %v", odd)
	}
}

// A database that was at 016 with the old names ends with exactly the index set
// of a fresh install.
func TestNeutralLeadIndexNames_ExistingInstall(t *testing.T) {
	ctx := context.Background()
	fresh, _ := scratchDB(t)
	if _, err := provider(t, fresh).Up(ctx); err != nil {
		t.Fatalf("fresh up: %v", err)
	}
	want := leadIndexes(t, fresh)

	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, 16); err != nil {
		t.Fatalf("up to 16: %v", err)
	}
	for _, s := range leadIndexSuffixes {
		if _, err := db.ExecContext(ctx, `ALTER INDEX idx_leads_`+s+` RENAME TO idx_`+oldSegment+`_leads_`+s); err != nil {
			t.Fatalf("restore old name: %v", err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up to head: %v", err)
	}
	if got := leadIndexes(t, db); !slices.Equal(got, want) {
		t.Errorf("index set after migrating differs from a fresh install:\n got: %q\nwant: %q", got, want)
	}
}

// The rename itself: a hatamoto_leads table that still exists at the previous
// head (017 drops it on a normal install) gets the neutral names, other indexes
// stay, a taken neutral name is left alone, and it is idempotent.
func TestNeutralLeadIndexNames_Renames(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, neutralLeadIndexesVersion-1); err != nil {
		t.Fatalf("up to previous head: %v", err)
	}
	stmts := []string{`CREATE TABLE public.hatamoto_leads (a int, b int, c int, d int)`}
	for i, s := range leadIndexSuffixes {
		col := string(rune('a' + i))
		stmts = append(stmts, `CREATE INDEX idx_`+oldSegment+`_leads_`+s+` ON public.hatamoto_leads (`+col+`)`)
	}
	// This one already has its neutral name taken: it must stay as it is.
	stmts = append(stmts,
		`CREATE INDEX idx_leads_email_normalized ON public.hatamoto_leads (a)`,
		`CREATE INDEX idx_leads_intent_tier ON public.hatamoto_leads (b)`)
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var names []string
	rows, err := db.QueryContext(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = 'hatamoto_leads' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	want := []string{
		"idx_leads_email_normalized", "idx_leads_intent_score", "idx_leads_intent_tier",
		"idx_leads_merged_into", "idx_leads_sales_engaged_at",
		"idx_" + oldSegment + "_leads_intent_tier",
	}
	if !slices.Equal(names, want) {
		t.Errorf("indexes = %q, want %q", names, want)
	}
}
