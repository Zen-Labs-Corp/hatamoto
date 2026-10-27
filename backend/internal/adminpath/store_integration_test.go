//go:build integration

package adminpath_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/migrations"
)

// scratchPool migrates a fresh database next to DATABASE_URL and returns a pool on it.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_adminpath_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var db *sql.DB = stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func seedRow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO hatamoto_site_config (company_name) VALUES ('Example')`); err != nil {
		t.Fatal(err)
	}
}

func TestStoreMissingRow(t *testing.T) {
	s := adminpath.NewStore(scratchPool(t))
	ctx := context.Background()
	if _, err := s.Get(ctx); !errors.Is(err, adminpath.ErrNoSiteConfig) {
		t.Errorf("Get = %v, want ErrNoSiteConfig", err)
	}
	if err := s.Set(ctx, "/k7qm-x2rf-9tdc", "cli"); !errors.Is(err, adminpath.ErrNoSiteConfig) {
		t.Errorf("Set = %v, want ErrNoSiteConfig", err)
	}
}

func TestStorePendingNull(t *testing.T) {
	pool := scratchPool(t)
	seedRow(t, pool)
	st, err := adminpath.NewStore(pool).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Path != nil || st.AckBy != nil || st.AckAt != nil || st.SetBy != nil || st.SetAt != nil {
		t.Errorf("fresh row = %+v, want all nil (pending)", st)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	pool := scratchPool(t)
	seedRow(t, pool)
	s := adminpath.NewStore(pool)
	ctx := context.Background()
	if err := s.SetAck(ctx, "/admin", "cli"); err != nil {
		t.Fatal(err)
	}
	st, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Path == nil || *st.Path != "/admin" || st.AckBy == nil || *st.AckBy != "cli" || st.AckAt == nil || st.AckAt.IsZero() {
		t.Errorf("after SetAck = %+v", st)
	}
	if st.SetBy == nil || *st.SetBy != "cli" || st.SetAt == nil || st.SetAt.IsZero() {
		t.Errorf("after SetAck = %+v, want set by cli with a time", st)
	}
}

func TestStoreAckKeepsWhoChose(t *testing.T) {
	pool := scratchPool(t)
	seedRow(t, pool)
	s := adminpath.NewStore(pool)
	ctx := context.Background()
	if err := s.Set(ctx, "/my-place", "Bea"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx)
	if err := s.Ack(ctx, "/other-place", "Ana"); !errors.Is(err, adminpath.ErrNoSiteConfig) {
		t.Errorf("Ack of another address = %v, want ErrNoSiteConfig", err)
	}
	if err := s.Ack(ctx, "/my-place", "Ana"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Get(ctx)
	if st.AckBy == nil || *st.AckBy != "Ana" || st.AckAt == nil {
		t.Errorf("after Ack = %+v", st)
	}
	if st.SetBy == nil || *st.SetBy != "Bea" || st.SetAt == nil || !st.SetAt.Equal(*before.SetAt) {
		t.Errorf("Ack changed who chose the address: %+v", st)
	}
}

func TestStoreSetClearsAck(t *testing.T) {
	pool := scratchPool(t)
	seedRow(t, pool)
	s := adminpath.NewStore(pool)
	ctx := context.Background()
	if err := s.SetAck(ctx, "/admin", "cli"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "/n3wq-8vhd-p2ka", "Ada"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Get(ctx)
	if st.Path == nil || *st.Path != "/n3wq-8vhd-p2ka" || st.AckBy != nil || st.AckAt != nil {
		t.Errorf("after Set = %+v, want new path and no ack", st)
	}
	if st.SetBy == nil || *st.SetBy != "Ada" || st.SetAt == nil || st.SetAt.IsZero() {
		t.Errorf("after Set = %+v, want set by Ada with a time", st)
	}
}

func TestStoreSetIfPendingOnce(t *testing.T) {
	pool := scratchPool(t)
	seedRow(t, pool)
	s := adminpath.NewStore(pool)
	ctx := context.Background()
	if ok, err := s.SetIfPending(ctx, "/first-value-1"); err != nil || !ok {
		t.Fatalf("first = %v, %v", ok, err)
	}
	if ok, err := s.SetIfPending(ctx, "/second-value-2"); err != nil || ok {
		t.Fatalf("second = %v, %v, want false", ok, err)
	}
	if st, _ := s.Get(ctx); st.Path == nil || *st.Path != "/first-value-1" || st.SetBy != nil || st.SetAt != nil {
		t.Errorf("stored = %+v, want first value with set by/at left NULL", st)
	}
}
