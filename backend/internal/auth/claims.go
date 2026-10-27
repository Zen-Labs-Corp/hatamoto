package auth

import (
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidToken is returned when a session or API-key credential is not valid.
var ErrInvalidToken = errors.New("invalid token")

// Claims is the authenticated principal a validated session puts in the
// request context.
type Claims struct {
	UserID       uuid.UUID `json:"user_id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	IsSuperadmin bool      `json:"is_superadmin,omitempty"`
}
