package auth

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/user"
)

// A user created without a password (empty stored hash) can never sign in
// with one: the answer is the neutral invalid-credentials error, not a 500.
func TestPasswordlessUserCannotSignInWithPassword(t *testing.T) {
	ctx := context.Background()
	u := newTestUser(user.RoleOwner, "")
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)

	if _, err := svc.CheckPassword(ctx, u.Email, "anything-at-all"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CheckPassword err = %v, want ErrInvalidCredentials", err)
	}
	if repo.recordFailedCalled {
		t.Fatal("a wrong password against a user with no password was counted: anyone could lock them out")
	}
	if _, _, err := svc.LoginAPISession(ctx, u.Email, "anything-at-all", "", "ua", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("LoginAPISession err = %v, want ErrInvalidCredentials", err)
	}
}

func TestVerifyStoredEmptyHashIsWrongPassword(t *testing.T) {
	ok, err := verifyStored(context.Background(), "whatever", "")
	if err != nil || ok {
		t.Fatalf("verifyStored(empty) = %v, %v; want false, nil", ok, err)
	}
}
