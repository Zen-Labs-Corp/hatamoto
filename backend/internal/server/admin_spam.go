package server

import (
	"context"

	"hatamoto.dev/core/internal/spam"
)

// spamAdmin is the admin's view of the spam settings: the store, the key that
// seals the captcha secret, and the provider check behind Test connection.
type spamAdmin struct {
	store    *spam.Store
	verifier *spam.HTTPVerifier
	key      []byte
}

func (a spamAdmin) Settings(ctx context.Context) (spam.Settings, error) {
	return a.store.Get(ctx)
}

func (a spamAdmin) Save(ctx context.Context, s spam.Settings) error {
	return a.store.Save(ctx, s)
}

func (a spamAdmin) SealSecret(secret string) ([]byte, error) {
	return spam.SealSecret(a.key, secret)
}

func (a spamAdmin) SecretReadable(s spam.Settings) bool {
	_, ok := s.Secret(a.key)
	return ok
}

func (a spamAdmin) TestSecret(ctx context.Context, s spam.Settings) (bool, string, error) {
	secret, ok := s.Secret(a.key)
	if !ok {
		return false, "There is no readable secret key saved.", nil
	}
	return a.verifier.TestSecret(ctx, s.Provider, secret, s.SiteKey)
}
