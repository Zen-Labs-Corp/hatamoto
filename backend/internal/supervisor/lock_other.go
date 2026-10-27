//go:build !unix

package supervisor

import "errors"

// WithLock is unsupported off unix: the supervisor is linux/darwin only.
func WithLock(path string, fn func() error) error {
	return errors.New("supervisor: unsupported platform")
}
