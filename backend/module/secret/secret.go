package secret

import (
	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/secretbox"
)

// ErrNoSecret is returned when the process has no server secret (for example
// a command that did not load it).
var ErrNoSecret = modhost.ErrNoSecret

// Seal encrypts plain for module and purpose, binding aad. The key is
// derived from the server secret under the domain module/<module>/<purpose>,
// so a module can never derive (or open) a key core uses.
func Seal(module, purpose string, aad, plain []byte) ([]byte, error) {
	key, err := modhost.ModuleKey(module, purpose)
	if err != nil {
		return nil, err
	}
	return secretbox.Seal(key, aad, plain)
}

// Open decrypts a blob made by Seal with the same module, purpose and aad.
func Open(module, purpose string, aad, blob []byte) ([]byte, error) {
	key, err := modhost.ModuleKey(module, purpose)
	if err != nil {
		return nil, err
	}
	return secretbox.Open(key, aad, blob)
}
