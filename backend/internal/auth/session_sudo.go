package auth

import (
	"context"

	"github.com/google/uuid"
)

// ConfirmPassword re-checks a signed-in user's password for sudo mode. It
// shares authenticate, so failures count toward the login lockout. The account
// that email resolves to must be userID, the session's user: any other account
// is refused as invalid credentials, so a lookup that drifts cannot elevate.
func (s *Service) ConfirmPassword(ctx context.Context, userID uuid.UUID, email, password string) error {
	u, err := s.authenticate(ctx, email, password)
	if err != nil {
		return err
	}
	if u.ID != userID {
		return ErrInvalidCredentials
	}
	return nil
}
