//go:build unix

package supervisor

import (
	"os"
	"syscall"
)

// WithLock runs fn holding an exclusive flock on the file at path (created if
// needed). The CLI and the supervisor both take it around state.json
// read-modify-write.
func WithLock(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|noFollow, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
