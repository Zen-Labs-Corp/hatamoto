//go:build !unix

package backup

import "sync"

var procMu sync.Mutex

// TryLock is the fallback where flock is not available: one backup per
// process, which is all a single-instance install needs.
func TryLock(dir string) (func(), error) {
	if !procMu.TryLock() {
		return nil, ErrBusy
	}
	return procMu.Unlock, nil
}
