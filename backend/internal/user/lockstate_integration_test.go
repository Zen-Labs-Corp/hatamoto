//go:build integration

package user_test

import (
	"context"
	"encoding/base32"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

func lockStateRepo(t *testing.T) (*user.PostgresRepository, *user.User) {
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
	email := "lock-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	u, err := repo.Create(ctx, &user.CreateUserInput{Email: email, Role: user.RoleEditor, Name: "L"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, u.ID)
	})
	return repo, u
}

func TestGetByIDLoadsLockState_Integration(t *testing.T) {
	repo, u := lockStateRepo(t)
	ctx := database.WithSuperadmin(context.Background())

	now := time.Now()
	svc := twofactor.NewService(twofactor.NewMemoryStore(), repo, twofactor.Config{
		SealKey:     twofactor.SealKeyFor("x"),
		RecoveryKey: twofactor.RecoveryKey("x"),
		MaxFailed:   5,
		Lock:        time.Minute,
		Now:         func() time.Time { return now },
	})

	secretB32, _, err := svc.BeginTOTP(ctx, u.ID, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTP(ctx, u.ID, twofactor.Code(secret, twofactor.StepAt(now))); err != nil {
		t.Fatal(err)
	}
	// A code for a later step, so the stored last step does not reject it.
	now = now.Add(time.Minute)
	good := twofactor.Code(secret, twofactor.StepAt(now))

	for range 5 {
		if err := repo.RecordFailedLogin(ctx, u.ID, 5, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	byID, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	byEmail, err := repo.GetByEmail(ctx, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	if byID.LockedUntil == nil || !byID.LockedUntil.After(now) {
		t.Fatalf("GetByID LockedUntil = %v, want a time after %v", byID.LockedUntil, now)
	}
	if byID.FailedLoginAttempts != 5 {
		t.Fatalf("GetByID FailedLoginAttempts = %d, want 5", byID.FailedLoginAttempts)
	}
	if byEmail.LockedUntil == nil || !byEmail.LockedUntil.Equal(*byID.LockedUntil) || byEmail.FailedLoginAttempts != byID.FailedLoginAttempts {
		t.Fatalf("GetByID and GetByEmail disagree on lock state: %v/%d vs %v/%d",
			byID.LockedUntil, byID.FailedLoginAttempts, byEmail.LockedUntil, byEmail.FailedLoginAttempts)
	}

	if err := svc.Verify(ctx, u.ID, good); !errors.Is(err, twofactor.ErrWrongCode) {
		t.Fatalf("Verify with a correct code on a locked account = %v, want ErrWrongCode", err)
	}
}
