//go:build integration

package analytics

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/migrations"
)

// testDB returns a handle on a scratch database migrated to head, dropped when
// the test ends, so tests share no rows.
func testDB(t *testing.T) *database.DB {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_analytics_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	pool.Close()
	db, err := database.New(ctx, config.DatabaseConfig{URL: u.String(), MaxConnections: 8, MinConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	return db
}

func TestSettingsEngagementRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := NewSettingsStore(testDB(t))
	if on, err := st.EngagementEnabled(ctx); err != nil || !on {
		t.Fatalf("default = %v, %v; want true", on, err)
	}
	for _, want := range []bool{false, true} {
		if err := st.SetEngagement(ctx, want); err != nil {
			t.Fatal(err)
		}
		if on, err := st.EngagementEnabled(ctx); err != nil || on != want {
			t.Fatalf("after set %v: %v, %v", want, on, err)
		}
	}
}
