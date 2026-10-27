package database

import (
	"context"

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
