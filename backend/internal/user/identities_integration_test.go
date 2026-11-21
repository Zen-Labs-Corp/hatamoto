//go:build integration

package user_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

func identityRig(t *testing.T) (context.Context, *database.DB, *user.IdentityStore, *user.User, *user.User) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := user.NewPostgresRepository(db)
	mk := func() *user.User {
		email := "ident-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
		u, err := repo.Create(ctx, &user.CreateUserInput{Email: email, Role: user.RoleEditor, Name: "I"}, "x")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, u.ID) })
		return u
	}
	return ctx, db, user.NewIdentityStore(db), mk(), mk()
}

func TestIdentityStore_LinkLookupUnlink(t *testing.T) {
	ctx, _, store, a, b := identityRig(t)
	issuer, subject := "https://idp.example.test/"+uuid.NewString(), "sub-1"

	if _, err := store.UserFor(ctx, issuer, subject); !errors.Is(err, user.ErrIdentityNotFound) {
		t.Fatalf("UserFor before link = %v, want ErrIdentityNotFound", err)
	}
	if err := store.Link(ctx, a.ID, issuer, subject); err != nil {
		t.Fatal(err)
	}
	if err := store.Link(ctx, a.ID, issuer, subject); err != nil {
		t.Errorf("linking the same pair to the same user again = %v, want nil", err)
	}
	if err := store.Link(ctx, b.ID, issuer, subject); !errors.Is(err, user.ErrIdentityTaken) {
		t.Errorf("linking to another user = %v, want ErrIdentityTaken", err)
	}
	if got, err := store.UserFor(ctx, issuer, subject); err != nil || got != a.ID {
		t.Errorf("UserFor = %v, %v; want %v", got, err, a.ID)
	}
	if err := store.Link(ctx, b.ID, issuer+"/other", subject); err != nil {
		t.Errorf("same subject under another issuer = %v, want nil", err)
	}
	if err := store.Unlink(ctx, b.ID, issuer, subject); !errors.Is(err, user.ErrIdentityNotFound) {
		t.Errorf("unlink by a non-owner = %v, want ErrIdentityNotFound", err)
	}
	if err := store.Unlink(ctx, a.ID, issuer, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UserFor(ctx, issuer, subject); !errors.Is(err, user.ErrIdentityNotFound) {
		t.Errorf("UserFor after unlink = %v, want ErrIdentityNotFound", err)
	}
}

func TestIdentityStore_TouchAndUserDelete(t *testing.T) {
	ctx, db, store, a, _ := identityRig(t)
	issuer, subject := "https://idp.example.test/"+uuid.NewString(), "sub-2"
	if err := store.Link(ctx, a.ID, issuer, subject); err != nil {
		t.Fatal(err)
	}
	lastUsed := func() (set bool) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM hatamoto_user_identities WHERE issuer = $1 AND subject = $2`, issuer, subject).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}
	if lastUsed() {
		t.Fatal("last_used_at set before Touch")
	}
	if err := store.Touch(ctx, issuer, subject); err != nil {
		t.Fatal(err)
	}
	if !lastUsed() {
		t.Error("last_used_at not set after Touch")
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UserFor(ctx, issuer, subject); !errors.Is(err, user.ErrIdentityNotFound) {
		t.Errorf("UserFor after user delete = %v, want ErrIdentityNotFound", err)
	}
}
