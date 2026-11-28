//go:build integration

package backupcrypt

import (
	"context"
	"errors"
	"os"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/backupcrypt/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_backup_encryption`)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_backup_encryption`)
		pool.Close()
	})
	return pool
}

func TestStoreRoundTrip(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	if _, found, err := st.Get(ctx); found || err != nil {
		t.Fatalf("empty: found = %v, err = %v", found, err)
	}
	ident, _ := age.GenerateX25519Identity()
	rec := ident.Recipient().String()
	if err := st.SavePublicKey(ctx, " "+rec+"\n"); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.Get(ctx)
	if err != nil || !found || got.Recipient != rec || got.Mode() != "key" || got.UpdatedAt.IsZero() {
		t.Fatalf("after key: %+v, %v, %v", got, found, err)
	}

	sealed, err := SealPassphrase(SealKey("server"), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePassphrase(ctx, sealed); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.Get(ctx)
	if err != nil || got.Recipient != "" || got.Mode() != "passphrase" {
		t.Fatalf("after passphrase: %+v, %v", got, err)
	}
	if err := st.SavePassphrase(ctx, nil); err == nil {
		t.Fatal("empty sealed passphrase accepted")
	}

	if err := st.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.Get(ctx); found || err != nil {
		t.Fatalf("after clear: found = %v, err = %v", found, err)
	}
}

func TestStoreRefusesPrivateKey(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	ident, _ := age.GenerateX25519Identity()
	if err := st.SavePublicKey(ctx, ident.String()); !errors.Is(err, ErrPrivateKey) {
		t.Fatalf("err = %v, want ErrPrivateKey", err)
	}
	if _, found, err := st.Get(ctx); found || err != nil {
		t.Fatalf("row written: found = %v, err = %v", found, err)
	}
}
