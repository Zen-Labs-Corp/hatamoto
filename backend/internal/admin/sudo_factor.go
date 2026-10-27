package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrWrongCode is what Verify returns for a code that is definitely wrong.
// Only this error counts toward the per-session cap. Any other error means
// the provider could not decide (an outage, a timeout) and is not counted.
var ErrWrongCode = errors.New("admin: wrong second-factor code")

// SecondFactor is the seam a second-factor provider (an authenticator app, say)
// plugs into. Core ships none: without one, sudo asks for the password only.
type SecondFactor interface {
	// Has reports whether the user enrolled a second factor. Return an error
	// when you cannot tell: sudo then refuses rather than skip the code.
	Has(ctx context.Context, userID uuid.UUID) (bool, error)
	// Verify checks the code the user typed. Return ErrWrongCode (or an error
	// wrapping it) for a wrong code, and any other error when you cannot tell.
	// Core only caps wrong codes per session (a handful per 15 minutes). A
	// provider should also count and lock its own failures per user, because a
	// session cap does not stop an attacker who opens new sessions.
	Verify(ctx context.Context, userID uuid.UUID, code string) error
}
