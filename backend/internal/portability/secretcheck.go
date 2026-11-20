package portability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"hatamoto.dev/core/internal/secretbox"
)

const (
	secretCheckDomain = "hatamoto/archive/check"
	secretCheckLabel  = "hatamoto-archive-check-v1"
)

// SecretCheck is the value an archive records so a restore can tell whether it
// runs under the same server secret. It is an HMAC of a fixed label, so it
// reveals nothing about the secret. "" for "".
func SecretCheck(secret string) string {
	if secret == "" {
		return ""
	}
	m := hmac.New(sha256.New, secretbox.DeriveKey(secret, secretCheckDomain))
	m.Write([]byte(secretCheckLabel))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SecretVerdict is the result of comparing an archive's check with the local secret.
type SecretVerdict int

const (
	SecretMatch    SecretVerdict = iota // same secret
	SecretNoCheck                       // the archive carries no check
	SecretUnknown                       // no local secret to compare with
	SecretMismatch                      // a different secret
)

// VerifySecret compares an archive's check value with the local secret.
func VerifySecret(check, secret string) SecretVerdict {
	switch {
	case check == "":
		return SecretNoCheck
	case secret == "":
		return SecretUnknown
	case hmac.Equal([]byte(check), []byte(SecretCheck(secret))):
		return SecretMatch
	}
	return SecretMismatch
}

// ErrSecretMismatch is returned when an archive was made under another secret.
var ErrSecretMismatch = errors.New("this archive was made with a different server secret: commenter emails in it can't be decrypted and comment hashes won't match. Restore with the original HATAMOTO_SECRET, or re-run with --force to import and drop the unreadable commenter emails")

// DropCommenterEmailsSQL blanks the sealed emails a different secret cannot
// open. email_sealed is NOT NULL; an empty value fails secretbox.Open
// ("damaged"), so Keys.OpenEmail reports it unreadable everywhere.
const DropCommenterEmailsSQL = `UPDATE public.hatamoto_commenters SET email_sealed = ''::bytea WHERE email_sealed <> ''::bytea`
