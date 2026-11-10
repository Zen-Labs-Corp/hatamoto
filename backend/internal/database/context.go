package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Context keys for user information.
type contextKey string

const (
	userContextKey       contextKey = "user_id"
	superadminContextKey contextKey = "is_superadmin"
)

// WithUser adds a user ID to the context.
func WithUser(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userContextKey, userID)
}

// GetUserID retrieves the user ID from the context.
func GetUserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userContextKey).(uuid.UUID)
	return id, ok
}

// WithSuperadmin marks the context as having superadmin privileges.
func WithSuperadmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, superadminContextKey, true)
}

// IsSuperadmin checks if the context has superadmin privileges.
func IsSuperadmin(ctx context.Context) bool {
	val, ok := ctx.Value(superadminContextKey).(bool)
	return ok && val
}

// ErrPoolBusy is returned when a bounded acquire (see WithAcquireBound) could
// not get a connection in time while the caller's own context was still alive.
var ErrPoolBusy = errors.New("database pool busy")

type acquireBoundKey struct{}

// WithAcquireBound marks ctx so that connection acquires made with it give up
// after d with ErrPoolBusy. d <= 0 returns ctx unchanged.
func WithAcquireBound(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, acquireBoundKey{}, d)
}

// AcquireBound returns the bound set by WithAcquireBound, or 0.
func AcquireBound(ctx context.Context) time.Duration {
	d, _ := ctx.Value(acquireBoundKey{}).(time.Duration)
	return d
}

// WithoutAcquireBound returns ctx with any acquire bound removed.
func WithoutAcquireBound(ctx context.Context) context.Context {
	if AcquireBound(ctx) == 0 {
		return ctx
	}
	return context.WithValue(ctx, acquireBoundKey{}, time.Duration(0))
}
