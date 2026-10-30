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
	for name, tc := range map[string]struct {
		rows []convEnt
		want bool
	}{"region trees": {convFixture(), true}, "nested site": {nested, false}} {
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
			got, err := regionTreeCandidates(pool)(ctx)
			if err != nil || got != tc.want {
				t.Errorf("regionTreeCandidates = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}
