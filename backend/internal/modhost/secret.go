package modhost

import (
	"errors"
	"fmt"
	"regexp"
	"sync/atomic"

	"hatamoto.dev/core/internal/secretbox"
)

// ErrNoSecret is returned when core has not installed a secret source (a
// process that never read the server secret).
var ErrNoSecret = errors.New("module: no server secret available")

type secretSource func() (string, error)

var (
	secretSrc     atomic.Pointer[secretSource]
	purposeFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

func init() { onReset(func() { secretSrc.Store(nil) }) }

// SetSecretSource installs the server secret reader; core only. The secret
// itself never leaves this package.
func SetSecretSource(f func() (string, error)) {
	if f == nil {
		panic("modhost: nil secret source")
	}
	s := secretSource(f)
	secretSrc.Store(&s)
}

// ModuleKey derives the key for module and purpose under the fixed domain
// "module/<module>/<purpose>", which no core key uses.
func ModuleKey(module, purpose string) ([]byte, error) {
	if !ValidName(module) {
		return nil, fmt.Errorf("module: invalid module name %q", module)
	}
	if !purposeFormat.MatchString(purpose) {
		return nil, fmt.Errorf("module: invalid secret purpose %q", purpose)
	}
	p := secretSrc.Load()
	if p == nil {
		return nil, ErrNoSecret
	}
	secret, err := (*p)()
	if err != nil {
		return nil, fmt.Errorf("module: read the server secret: %w", err)
	}
	if secret == "" {
		return nil, ErrNoSecret
	}
	return secretbox.DeriveKey(secret, "module/"+module+"/"+purpose), nil
}
