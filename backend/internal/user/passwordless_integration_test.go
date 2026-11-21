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

func passwordlessRig(t *testing.T) (context.Context, *database.DB, *user.PostgresRepository) {
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
	return ctx, db, user.NewPostgresRepository(db)
}

func createPasswordless(t *testing.T, ctx context.Context, db *database.DB, repo *user.PostgresRepository, email string) *user.User {
	t.Helper()
	u, err := repo.Create(ctx, &user.CreateUserInput{Email: email, Role: user.RoleEditor, Name: "P"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, u.ID) })
	return u
}

func TestPasswordless_StoredAsNullAndLoads(t *testing.T) {
	ctx, db, repo := passwordlessRig(t)
	email := "pl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	u := createPasswordless(t, ctx, db, repo, email)

	var isNull bool
	if err := db.Pool().QueryRow(ctx, `SELECT password_hash IS NULL FROM hatamoto_users WHERE id = $1`, u.ID).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("password_hash is not NULL for a user created with an empty hash")
	}
	byID, err := repo.GetByID(ctx, u.ID)
	if err != nil || byID.PasswordHash != "" {
		t.Errorf("GetByID = %+v, %v; want empty PasswordHash", byID, err)
	}
	byEmail, err := repo.GetByEmail(ctx, email)
	if err != nil || byEmail.PasswordHash != "" {
		t.Errorf("GetByEmail = %+v, %v; want empty PasswordHash", byEmail, err)
	}
	list, err := repo.ListByEmail(ctx, email)
	if err != nil || len(list) != 1 || list[0].PasswordHash != "" {
		t.Errorf("ListByEmail = %+v, %v; want one user with empty PasswordHash", list, err)
	}
	if err := repo.SetPassword(ctx, u.ID, "hash-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetByID(ctx, u.ID); err != nil || got.PasswordHash != "hash-1" {
		t.Errorf("after SetPassword: %+v, %v; want hash-1", got, err)
	}
}

func TestPasswordless_GetByEmailFold(t *testing.T) {
	ctx, db, repo := passwordlessRig(t)
	local := "fold" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	u := createPasswordless(t, ctx, db, repo, local+"@example.test")

	got, err := repo.GetByEmailFold(ctx, "  "+strings.ToUpper(local)+"@Example.test ")
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetByEmailFold = %v, %v; want %v", got, err, u.ID)
	}
	if _, err := repo.GetByEmailFold(ctx, "nobody-"+local+"@example.test"); !errors.Is(err, user.ErrUserNotFound) {
		t.Errorf("unknown email = %v, want ErrUserNotFound", err)
	}
	createPasswordless(t, ctx, db, repo, strings.ToUpper(local)+"@example.test")
	if _, err := repo.GetByEmailFold(ctx, local+"@example.test"); !errors.Is(err, user.ErrEmailAmbiguous) {
		t.Errorf("two case variants = %v, want ErrEmailAmbiguous", err)
	}
}
