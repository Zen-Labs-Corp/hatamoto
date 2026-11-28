// Package backupcrypt encrypts backup archives with age for off-site copies and
// downloads, and decrypts them for restore. It is the only package that imports
// filippo.io/age.
package backupcrypt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

// Magic is the first line of every age file.
const Magic = "age-encryption.org/v1"

const (
	privatePrefix = "AGE-SECRET-KEY-"
	minPassphrase = 12
)

var (
	// ErrWrongKey means the identity or passphrase does not open the backup.
	ErrWrongKey = errors.New("the private key or passphrase does not open this backup")
	// ErrPrivateKey means a private key was given where a public key belongs.
	ErrPrivateKey = errors.New("this is a private key; paste the public key (it starts with age1) and keep the private key off this server")
	// ErrNoKey means an encrypted backup was given without a key.
	ErrNoKey = errors.New("the backup is encrypted: give the private key file or the passphrase")
)

// scryptWorkFactor is the age scrypt cost for passphrase encryption; tests lower it.
var scryptWorkFactor = 18

// Key is what a backup is encrypted to: one age public key or one passphrase.
type Key struct {
	r          age.Recipient
	passphrase bool
}

// ParseRecipient parses an age public key (age1...). A private key is refused
// with ErrPrivateKey. The error never echoes the input.
func ParseRecipient(s string) (*Key, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToUpper(s), privatePrefix) {
		return nil, ErrPrivateKey
	}
	r, err := age.ParseX25519Recipient(s)
	if err != nil {
		return nil, errors.New("not an age public key (it starts with age1)")
	}
	return &Key{r: r}, nil
}

// ValidatePassphrase checks the passphrase length.
func ValidatePassphrase(p string) error {
	if len([]rune(p)) < minPassphrase {
		return fmt.Errorf("the passphrase needs at least %d characters", minPassphrase)
	}
	return nil
}

// PassphraseKey returns a Key that encrypts with a passphrase.
func PassphraseKey(p string) (*Key, error) {
	if err := ValidatePassphrase(p); err != nil {
		return nil, err
	}
	r, err := age.NewScryptRecipient(p)
	if err != nil {
		return nil, errors.New("the passphrase cannot be used")
	}
	r.SetWorkFactor(scryptWorkFactor)
	return &Key{r: r, passphrase: true}, nil
}

// IsPassphrase reports whether the key is a passphrase.
func (k *Key) IsPassphrase() bool { return k.passphrase }

// Encrypt returns a writer that encrypts into w. Close it to finish the stream.
func (k *Key) Encrypt(w io.Writer) (io.WriteCloser, error) { return age.Encrypt(w, k.r) }

// EncryptFile encrypts src into the new file dst (mode 0600). dst must not
// exist; it is removed on any error.
func (k *Key) EncryptFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()
	w, err := k.Encrypt(out)
	if err != nil {
		out.Close()
		return err
	}
	if _, err = io.Copy(w, in); err != nil {
		out.Close()
		return err
	}
	if err = w.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Identity is what opens an encrypted backup: an age identity file or a passphrase.
type Identity struct{ ids []age.Identity }

// ParseIdentityFile reads an age identity file (comments allowed). It never
// echoes the content in an error.
func ParseIdentityFile(r io.Reader) (*Identity, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil, err
	}
	ids, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		if hasPublicOnly(string(data)) {
			return nil, errors.New("this file holds a public key; restore needs the private key (AGE-SECRET-KEY-...)")
		}
		return nil, errors.New("not an age identity file")
	}
	if len(ids) == 0 {
		return nil, errors.New("the identity file holds no private key")
	}
	return &Identity{ids: ids}, nil
}

func hasPublicOnly(text string) bool {
	public := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(strings.ToUpper(line), privatePrefix):
			return false
		case strings.HasPrefix(line, "age1"):
			public = true
		}
	}
	return public
}

// PassphraseIdentity returns an Identity that opens passphrase-encrypted backups.
func PassphraseIdentity(p string) (*Identity, error) {
	i, err := age.NewScryptIdentity(p)
	if err != nil {
		return nil, errors.New("the passphrase cannot be used")
	}
	return &Identity{ids: []age.Identity{i}}, nil
}

// DecryptFile decrypts src into the new file dst (mode 0600). A wrong key gives
// ErrWrongKey. dst is removed on any error.
func (id *Identity) DecryptFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	plain, err := age.Decrypt(bufio.NewReader(in), id.ids...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return ErrWrongKey
		}
		return fmt.Errorf("the backup cannot be read as an age file: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()
	if _, err = io.Copy(out, plain); err != nil {
		out.Close()
		return fmt.Errorf("the backup is damaged: %w", err)
	}
	return out.Close()
}

// IsEncrypted reports whether the file starts with the age header.
func IsEncrypted(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, len(Magic)+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return string(buf[:n]) == Magic+"\n", nil
}
