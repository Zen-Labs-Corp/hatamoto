package spam

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/secretbox"
)

const (
	commentTokenDomain = "hatamoto/spam/comment-token"
	// CommentTokenMaxAge is how long a comment form token stays usable.
	CommentTokenMaxAge = 4 * time.Hour
	commentNonceBytes  = 12
	commentTokenMaxLen = 256
)

// CommentTokenKey derives the comment form token key from the server secret.
func CommentTokenKey(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, commentTokenDomain)
}

// TokenStore records used single-use token ids. fresh is false for a replay.
type TokenStore interface {
	ConsumeFormToken(ctx context.Context, tokenID string, expiresAt time.Time) (fresh bool, err error)
}

// TokenResult is the outcome of CheckCommentToken.
type TokenResult int

const (
	TokenOK TokenResult = iota
	TokenInvalid
	TokenExpired
	TokenReplayed
)

// IssueCommentToken returns a single-use token bound to a subject and a
// hashed visitor address: v1.<unix>.<nonce>.<mac>. The random nonce makes two
// tokens issued in the same second differ.
func IssueCommentToken(key []byte, subject, ipHash string, now time.Time) string {
	nb := make([]byte, commentNonceBytes)
	if _, err := rand.Read(nb); err != nil {
		panic("spam: crypto/rand failed: " + err.Error())
	}
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	ts := strconv.FormatInt(now.Unix(), 10)
	return "v1." + ts + "." + nonce + "." + commentTokenMAC(key, subject, ipHash, nonce, ts)
}

// CheckCommentToken verifies tok and marks it used. It returns when the token
// was issued so the caller can score the dwell time. A store failure is
// TokenInvalid (fail closed). The minimum dwell is not checked here.
func CheckCommentToken(ctx context.Context, key []byte, store TokenStore, subject, ipHash, tok string, now time.Time) (issued time.Time, res TokenResult) {
	if len(tok) > commentTokenMaxLen {
		return time.Time{}, TokenInvalid
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return time.Time{}, TokenInvalid
	}
	ts, nonce := parts[1], parts[2]
	if !hmac.Equal([]byte(commentTokenMAC(key, subject, ipHash, nonce, ts)), []byte(parts[3])) {
		return time.Time{}, TokenInvalid
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, TokenInvalid
	}
	issued = time.Unix(sec, 0)
	if issued.After(now.Add(tokenSkew)) {
		return time.Time{}, TokenInvalid
	}
	if now.Sub(issued) > CommentTokenMaxAge {
		return time.Time{}, TokenExpired
	}
	sum := sha256.Sum256([]byte(nonce))
	fresh, err := store.ConsumeFormToken(ctx, hex.EncodeToString(sum[:]), issued.Add(CommentTokenMaxAge))
	if err != nil {
		return time.Time{}, TokenInvalid
	}
	if !fresh {
		return time.Time{}, TokenReplayed
	}
	return issued, TokenOK
}

func commentTokenMAC(key []byte, subject, ipHash, nonce, ts string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("c|" + subject + "|" + ipHash + "|" + nonce + "|" + ts))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
