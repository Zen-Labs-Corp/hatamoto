//go:build integration

package server

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRegionTreeCandidates_Integration: the notice on Settings > General shows
// for a site that keeps each region in its own page tree and not for an
// ordinary nested site.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RegionTreeCandidates
func TestRegionTreeCandidates_Integration(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set - skipping region tree candidate test")
	}
	nested := []convEnt{
		{"n1", "page", "services", "", "published", `{}`},
		{"n2", "page", "web", "n1", "published", `{}`},
		{"n3", "page", "about", "", "published", `{}`},
		{"n4", "page", "team", "n3", "published", `{}`},
	}
	// Top-level pages with children whose attributes are not a JSON object.
	odd := []convEnt{
		{"o1", "page", "arr", "", "published", `{}`},
		{"o2", "page", "arr-child", "o1", "published", `{}`},
		{"o3", "page", "scalar", "", "published", `{}`},
		{"o4", "page", "scalar-child", "o3", "published", `{}`},
	}
	oddAttrs := map[string]string{"o1": `[]`, "o3": `"x"`}
	for name, tc := range map[string]struct {
		rows []convEnt
		want bool
	}{
		"region trees":                 {convFixture(), true},
		"nested site":                  {nested, false},
		"region trees, odd attributes": {append(convFixture(), odd...), true},
		"odd attributes only":          {odd, false},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			pool, err := pgxpool.New(ctx, scratchMigratedDB(t, base))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			convSeed(t, func(q string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, q, args...); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}, tc.rows)
			for _, e := range tc.rows {
				if raw, ok := oddAttrs[e.id]; ok {
					if _, err := pool.Exec(ctx, `UPDATE hatamoto_entities SET attributes = $2::jsonb WHERE id = $1`, convID(e.id), raw); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := regionTreeCandidates(pool)(ctx)
			if err != nil || got != tc.want {
				t.Errorf("regionTreeCandidates = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}
