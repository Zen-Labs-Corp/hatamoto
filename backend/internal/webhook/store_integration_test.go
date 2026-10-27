//go:build integration

package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	name := "hm_webhook_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
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

func testService(t *testing.T) (*Service, *database.DB) {
	t.Helper()
	db := testDB(t)
	return NewService(db, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}

func TestCreateReturnsSecretOnce(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	created, secret, err := svc.Create(ctx, validSub())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, SecretPrefix) || len(secret) != len(SecretPrefix)+64 {
		t.Fatalf("secret = %q", secret)
	}
	if created.ID == "" || created.Secret != "" {
		t.Fatalf("created = %+v: want an id and no secret in the returned row", created)
	}
	stored, err := svc.Get(ctx, created.ID)
	if err != nil || stored.Secret != secret {
		t.Fatalf("Get: %v, secret %q", err, stored.Secret)
	}
	bad := validSub()
	bad.URL = "ftp://x"
	var ve *ValidationError
	if _, _, err := svc.Create(ctx, bad); !errors.As(err, &ve) {
		t.Fatalf("invalid create: %v", err)
	}
}

func TestListedSubscriptionCannotCarryASecret(t *testing.T) {
	if _, ok := reflect.TypeOf(ListedSubscription{}).FieldByName("Secret"); ok {
		t.Fatal("ListedSubscription has a Secret field")
	}
	svc, db := testService(t)
	ctx := context.Background()
	created, secret, err := svc.Create(ctx, validSub())
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range []string{"delivered", "delivered", "failed", "pending"} {
		if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status)
			VALUES ($1, $2, 'entity.created', '{}', $3)`, uuid.NewString(), created.ID, st); err != nil {
			t.Fatal(i, err)
		}
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %d", err, len(list))
	}
	if list[0].Stats != (Stats{Delivered: 2, Failed: 1, Pending: 1}) {
		t.Fatalf("stats = %+v", list[0].Stats)
	}
	if strings.Contains(strings.Join([]string{list[0].ID, list[0].Name, list[0].URL}, " "), secret) {
		t.Fatal("secret leaked into a list row")
	}
}

func TestUpdateMergesAndKeepsTheSecret(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	created, secret, err := svc.Create(ctx, validSub())
	if err != nil {
		t.Fatal(err)
	}
	name, retries := "renamed", 7
	if err := svc.Update(ctx, created.ID, SubscriptionPatch{Name: &name, MaxRetries: &retries}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, created.ID)
	if got.Name != "renamed" || got.MaxRetries != 7 || got.URL != created.URL || got.Secret != secret {
		t.Fatalf("after update: %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatal("updated_at did not move")
	}
	badRetries := 99
	var ve *ValidationError
	if err := svc.Update(ctx, created.ID, SubscriptionPatch{MaxRetries: &badRetries}); !errors.As(err, &ve) {
		t.Fatalf("invalid update: %v", err)
	}
	if err := svc.Update(ctx, uuid.NewString(), SubscriptionPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestRegenerateChangesTheSecret(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	created, old, _ := svc.Create(ctx, validSub())
	fresh, err := svc.RegenerateSecret(ctx, created.ID)
	if err != nil || fresh == old || !strings.HasPrefix(fresh, SecretPrefix) {
		t.Fatalf("regenerate: %v %q", err, fresh)
	}
	if got, _ := svc.Get(ctx, created.ID); got.Secret != fresh {
		t.Fatal("stored secret is not the new one")
	}
	if _, err := svc.RegenerateSecret(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestDeleteCascadesDeliveries(t *testing.T) {
	svc, db := testService(t)
	ctx := context.Background()
	created, _, _ := svc.Create(ctx, validSub())
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload)
		VALUES ($1, $2, 'entity.created', '{}')`, uuid.NewString(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_webhook_deliveries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("deliveries left: %d (%v)", n, err)
	}
	if err := svc.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFiftyFirstCreateFails(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	for i := 0; i < MaxSubscriptions; i++ {
		if _, _, err := svc.Create(ctx, validSub()); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	_, _, err := svc.Create(ctx, validSub())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Msg != "at most 50 subscriptions" {
		t.Fatalf("51st create: %v", err)
	}
}

func TestSetActive(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	created, _, _ := svc.Create(ctx, validSub())
	if err := svc.SetActive(ctx, created.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, created.ID); got.Active {
		t.Fatal("still active")
	}
	if err := svc.SetActive(ctx, uuid.NewString(), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
