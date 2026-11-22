package reviewstate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// GroupResolver says who may decide a stage whose Group is not DefaultGroup.
type GroupResolver interface {
	// MayDecide reports whether the user may decide a stage owned by group for entityID.
	// An error or a panic denies. It must honour ctx.
	MayDecide(ctx context.Context, userID, role, group, entityID string) (bool, error)
}

// ErrGroupResolver wraps a resolver error or panic: the decision is refused.
var ErrGroupResolver = errors.New("review: group resolver failed")

type groupReg struct{ r GroupResolver }

var groups atomic.Pointer[groupReg]

// RegisterGroups installs the group resolver. It shares the lock and the
// frozen flag with Register, so one Freeze covers both. It panics after
// Freeze, on a second call, and with a nil (or typed-nil) resolver.
func RegisterGroups(r GroupResolver) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("review: RegisterGroups called after app.Main started; register from an init function")
	case groups.Load() != nil:
		panic("review: RegisterGroups called twice")
	case isNil(r):
		panic("review: RegisterGroups with a nil resolver")
	}
	groups.Store(&groupReg{r: r})
}

// GroupsRegistered reports whether a group resolver is installed.
func GroupsRegistered() bool { return groups.Load() != nil }

// MayDecide asks the registered resolver. Without one it returns false, nil.
// A resolver error or panic yields false and an error wrapping ErrGroupResolver.
func MayDecide(ctx context.Context, userID, role, group, entityID string) (ok bool, err error) {
	g := groups.Load()
	if g == nil {
		return false, nil
	}
	defer func() {
		if v := recover(); v != nil {
			slog.Error("review: group resolver panicked", "group", group, "panic", v)
			ok, err = false, fmt.Errorf("%w: panic: %v", ErrGroupResolver, v)
		}
	}()
	ok, err = g.r.MayDecide(ctx, userID, role, group, entityID)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrGroupResolver, err)
	}
	return ok, nil
}
