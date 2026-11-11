package database

import (
	"context"
	"errors"
	"sync/atomic"
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

// acquireBound is the per-request state behind WithAcquireBound: the bound and
// a flag set whenever a bounded acquire or shared-load wait gave up with
// ErrPoolBusy, even if the caller swallowed the error and rendered without
// the data.
type acquireBound struct {
	d   time.Duration
	hit atomic.Bool
}

// WithAcquireBound marks ctx so that connection acquires made with it give up
// after d with ErrPoolBusy. d <= 0 returns ctx unchanged.
func WithAcquireBound(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, acquireBoundKey{}, &acquireBound{d: d})
}

// AcquireBound returns the bound set by WithAcquireBound, or 0.
func AcquireBound(ctx context.Context) time.Duration {
	if b, _ := ctx.Value(acquireBoundKey{}).(*acquireBound); b != nil {
		return b.d
	}
	return 0
}

// NotePoolBusy records that a bounded acquire on ctx's request gave up. It is a
// no-op without a bound.
func NotePoolBusy(ctx context.Context) {
	if b, _ := ctx.Value(acquireBoundKey{}).(*acquireBound); b != nil {
		b.hit.Store(true)
	}
}

// PoolBusyHit reports whether any bounded acquire or shared-load wait of the
// request owning ctx gave up with ErrPoolBusy, so the output built from it may
// be missing data and must not be cached or shared.
func PoolBusyHit(ctx context.Context) bool {
	b, _ := ctx.Value(acquireBoundKey{}).(*acquireBound)
	return b != nil && b.hit.Load()
}

// WithoutAcquireBound returns ctx with any acquire bound removed.
func WithoutAcquireBound(ctx context.Context) context.Context {
	if AcquireBound(ctx) == 0 {
		return ctx
	}
	return context.WithValue(ctx, acquireBoundKey{}, (*acquireBound)(nil))
}
