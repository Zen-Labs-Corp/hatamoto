package backupcrypt

import (
	"errors"
	"time"

	"hatamoto.dev/core/internal/secretbox"
)

const (
	passphraseDomain = "hatamoto/backup/encryption-passphrase"
	passphraseAAD    = "backup-encryption:passphrase"
)

// ErrUnreadable means encryption is on but its key cannot be used. Callers fail
// closed: nothing is uploaded or downloaded in clear.
var ErrUnreadable = errors.New("backup encryption is on but its key cannot be read; set it again under Settings > Backups")

// Settings is the saved backup encryption: an age public key, or a passphrase
// sealed with the server secret. Both empty means off.
type Settings struct {
	Recipient        string
	PassphraseSealed []byte
	UpdatedAt        time.Time
}

// Mode is "key", "passphrase" or "off".
func (s Settings) Mode() string {
	switch {
	case s.Recipient != "":
		return "key"
	case len(s.PassphraseSealed) > 0:
		return "passphrase"
	}
	return "off"
}

// SealKey derives the key that seals the passphrase from the server secret.
func SealKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, passphraseDomain) }

// SealPassphrase validates p and returns its sealed form for Settings.PassphraseSealed.
func SealPassphrase(sealKey []byte, p string) ([]byte, error) {
	if err := ValidatePassphrase(p); err != nil {
		return nil, err
	}
	return secretbox.Seal(sealKey, []byte(passphraseAAD), []byte(p))
}

// Key returns the key to encrypt with: nil, nil when encryption is off, and
// ErrUnreadable when the sealed passphrase does not open or the recipient no
// longer parses.
func (s Settings) Key(sealKey []byte) (*Key, error) {
	switch s.Mode() {
	case "key":
		k, err := ParseRecipient(s.Recipient)
		if err != nil {
			return nil, ErrUnreadable
		}
		return k, nil
	case "passphrase":
		p, err := secretbox.Open(sealKey, []byte(passphraseAAD), s.PassphraseSealed)
		if err != nil {
			return nil, ErrUnreadable
		}
		k, err := PassphraseKey(string(p))
		if err != nil {
			return nil, ErrUnreadable
		}
		return k, nil
	}
	return nil, nil
}
