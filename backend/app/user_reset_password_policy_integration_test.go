//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

type refusePasswordPolicy struct{}

func (refusePasswordPolicy) AllowSignIn(_ context.Context, a authstate.SignInAttempt) (bool, error) {
	return a.Method != authstate.MethodPassword, nil
}

// The CLI password reset is policy-free: it works whatever the sign-in policy
// says (also for a user created without a password), while the same password
// still cannot sign in through the service.
func TestResetPasswordIgnoresSignInPolicy(t *testing.T) {
	dsn := scratchDSN(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	repo := user.NewPostgresRepository(db)
	if _, err := repo.Create(ctx, &user.CreateUserInput{Email: "ana@example.test", Role: user.RoleOwner}, ""); err != nil {
		t.Fatal(err)
	}

	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterSignInPolicy(refusePasswordPolicy{})

	const pw = "a-long-new-password-1"
	var out bytes.Buffer
	if err := runUserResetPassword(ctx, db, &userFlags{email: "ana@example.test", password: pw}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("reset under a refusing policy: %v", err)
	}
	if !strings.Contains(out.String(), "password reset for ana@example.test") {
		t.Fatalf("output: %q", out.String())
	}
	got, err := repo.GetByEmail(ctx, "ana@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := auth.VerifyPassword(pw, got.PasswordHash); err != nil || !ok {
		t.Fatalf("stored hash does not verify the new password: %v %v", ok, err)
	}
	svc := auth.NewService(repo, nil, session.NewStore(db))
	if _, err := svc.CheckPassword(ctx, "ana@example.test", pw); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("CheckPassword under the policy: %v, want ErrInvalidCredentials", err)
	}
}
