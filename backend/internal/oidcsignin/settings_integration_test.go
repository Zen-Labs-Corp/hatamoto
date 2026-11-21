//go:build integration

package oidcsignin

import (
	"context"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

// testPool returns a pool on a scratch database migrated to head.
func testPool(t *testing.T) *pgxpool.Pool {
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
	name := "hm_oidc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
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
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	return pool
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := NewStore(testPool(t))
	if _, found, err := st.Get(ctx); err != nil || found {
		t.Fatalf("empty Get = found %v, err %v", found, err)
	}
	sealed, err := SealSecret(SecretKey("server-secret"), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		Enabled: true, Label: "Acme", Issuer: "https://idp.example.com/", ClientID: "cid",
		SecretSealed: sealed, AllowedDomains: []string{"acme.com", "example.org"},
		DefaultRole: "owner", LinkExisting: true,
	}
	if err := st.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.Get(ctx)
	if err != nil || !found {
		t.Fatalf("Get = found %v, err %v", found, err)
	}
	got.UpdatedAt = want.UpdatedAt
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if s, ok := got.Secret(SecretKey("server-secret")); !ok || s != "s3cret" {
		t.Fatal("sealed secret did not round trip")
	}

	// A second save replaces the single row; empty domains stay an empty list.
	want = Settings{DefaultRole: "editor"}
	if err := st.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.Get(ctx)
	if err != nil || got.Enabled || len(got.AllowedDomains) != 0 || got.HasSecret() {
		t.Fatalf("after reset: %+v, %v", got, err)
	}
	if err := st.Save(ctx, Settings{Enabled: true, DefaultRole: "editor"}); err == nil {
		t.Fatal("saved an incomplete enabled configuration")
	}
}
