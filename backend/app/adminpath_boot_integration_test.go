//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/adminpath"
)

func bootResolve(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	hasOwner := func(ctx context.Context) (bool, error) {
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_users`).Scan(&n)
		return n > 0, err
	}
	p, err := resolveAdminPath(ctx, adminpath.NewStore(pool), staticSite{}, hasOwner, "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A fresh install gets a strong random address that survives the next boot.
func TestFreshBootStoresRandomStrong(t *testing.T) {
	pool, ctx := scratchPool(t), context.Background()
	var first string
	for i := range 2 {
		if err := migrateUp(ctx, pool, quietLogger()); err != nil {
			t.Fatal(err)
		}
		if err := firstBoot(ctx, pool, "Site", quietLogger()); err != nil {
			t.Fatal(err)
		}
		got := bootResolve(t, pool)
		if i == 0 {
			first = got
			if res := adminpath.Assess(ctx, got, nil); res.Level != adminpath.LevelStrong {
				t.Fatalf("%q is %v, want strong", got, res.Level)
			}
		} else if got != first {
			t.Fatalf("second boot = %q, want %q", got, first)
		}
	}
}

// An install from before the admin address existed keeps /admin.
func TestUpgradeBootKeepsAdmin(t *testing.T) {
	pool, ctx := scratchPool(t), context.Background()
	const adminPathVersion = 202610261900
	err := withProvider(pool, func(p *goose.Provider) error {
		_, err := p.UpTo(ctx, adminPathVersion-1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Old')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_users (id, email, role) VALUES (gen_random_uuid(), 'o@example.test', 'owner')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, pool, "Old", quietLogger()); err != nil {
		t.Fatal(err)
	}
	if got := bootResolve(t, pool); got != "/admin" {
		t.Fatalf("got %q, want /admin", got)
	}
	// Even with the column NULL and an owner present, the answer is /admin.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_site_config SET admin_path = NULL`); err != nil {
		t.Fatal(err)
	}
	if got := bootResolve(t, pool); got != "/admin" {
		t.Fatalf("pending with owner: got %q, want /admin", got)
	}
}
