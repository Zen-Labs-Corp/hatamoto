package spam

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/secretbox"
)

const (
	tokenDomain = "hatamoto/spam/timing-token"
	// TokenMaxAge is how long an issued timing token stays usable.
	TokenMaxAge = 24 * time.Hour
	// tokenSkew tolerates a little clock difference between instances.
	tokenSkew = time.Minute
)

// TimingKey derives the timing token key from the server secret.
func TimingKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, tokenDomain) }

// IssueToken returns a stateless token proving when a visitor loaded a form or
// comment box: v1.<unix seconds>.<base64url HMAC>. The subject key is a form's
// slug as-is, or CommentSubject(id).
func IssueToken(key []byte, subject string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return "v1." + ts + "." + tokenMAC(key, subject, ts)
}

// CheckToken returns when tok was issued. ok is false for a malformed, forged,
// future-dated or expired token; callers then treat the timing as missing.
// subject is the same key the token was issued for.
func CheckToken(key []byte, subject, tok string, now time.Time) (issued time.Time, ok bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return time.Time{}, false
	}
	if !hmac.Equal([]byte(tokenMAC(key, subject, parts[1])), []byte(parts[2])) {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	issued = time.Unix(sec, 0)
	if issued.After(now.Add(tokenSkew)) || now.Sub(issued) > TokenMaxAge {
		return time.Time{}, false
	}
	return issued, true
}

func tokenMAC(key []byte, subject, ts string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("t|" + subject + "|" + ts))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
