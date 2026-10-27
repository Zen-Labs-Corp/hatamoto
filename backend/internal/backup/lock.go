package backup

import (
	"context"
	"errors"
	"time"
)

// ErrBusy means another backup holds the lock.
var ErrBusy = errors.New("another backup is running")

// DefaultWait is how long a pre-update backup waits for a running one.
const DefaultWait = 10 * time.Minute

// WaitLock polls TryLock every 250 ms until it succeeds, ctx ends or wait
// passes (then ErrBusy).
func WaitLock(ctx context.Context, dir string, wait time.Duration) (func(), error) {
	release, err := TryLock(dir)
	if !errors.Is(err, ErrBusy) {
		return release, err
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, ErrBusy
		case <-tick.C:
			release, err := TryLock(dir)
			if !errors.Is(err, ErrBusy) {
				return release, err
			}
		}
	}
}
