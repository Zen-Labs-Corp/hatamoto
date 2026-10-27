package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/core/internal/user"
)

func TestService_ConfirmPassword(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	t.Run("right password", func(t *testing.T) {
		t.Parallel()
		u := newTestUser(user.RoleOwner, hash)
		repo := &mockUserRepo{getByEmailReturn: u}
		svc := NewService(repo, nil, nil)

		testutil.RequireNoError(t, svc.ConfirmPassword(context.Background(), u.ID, u.Email, "correct-password"))
		testutil.False(t, repo.recordFailedCalled)
	})

	t.Run("wrong password counts toward the lockout", func(t *testing.T) {
		t.Parallel()
		u := newTestUser(user.RoleOwner, hash)
		repo := &mockUserRepo{getByEmailReturn: u}
		svc := NewService(repo, nil, nil)

		err := svc.ConfirmPassword(context.Background(), u.ID, u.Email, "wrong")
		testutil.RequireErrorIs(t, err, ErrInvalidCredentials)
		testutil.True(t, repo.recordFailedCalled, "a wrong password records a failed attempt")
		testutil.Equal(t, u.ID, repo.recordFailedID)
	})

	t.Run("locked account", func(t *testing.T) {
		t.Parallel()
		u := newTestUser(user.RoleOwner, hash)
		future := time.Now().Add(10 * time.Minute)
		u.LockedUntil = &future
		repo := &mockUserRepo{getByEmailReturn: u}
		svc := NewService(repo, nil, nil)

		err := svc.ConfirmPassword(context.Background(), u.ID, u.Email, "correct-password")
		testutil.RequireErrorIs(t, err, ErrAccountLocked)
	})

	t.Run("another user's account is refused", func(t *testing.T) {
		t.Parallel()
		u := newTestUser(user.RoleOwner, hash)
		repo := &mockUserRepo{getByEmailReturn: u}
		svc := NewService(repo, nil, nil)

		err := svc.ConfirmPassword(context.Background(), uuid.New(), u.Email, "correct-password")
		testutil.RequireErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("unknown email", func(t *testing.T) {
		t.Parallel()
		repo := &mockUserRepo{getByEmailErr: user.ErrUserNotFound}
		svc := NewService(repo, nil, nil)

		err := svc.ConfirmPassword(context.Background(), uuid.New(), "nobody@example.com", "correct-password")
		testutil.RequireErrorIs(t, err, ErrInvalidCredentials)
	})
}
