// Package secret lets a wrapper module encrypt small values (tokens, keys,
// credentials) at rest with a key derived from the server secret.
//
//	blob, err := secret.Seal("reports", "api-token", []byte("row-id"), []byte(token))
//	token, err := secret.Open("reports", "api-token", []byte("row-id"), blob)
//
// module is the module's own name ([a-z][a-z0-9_]{0,30}); purpose names the
// use ([a-z0-9][a-z0-9._-]{0,63}). The key domain is always
// module/<module>/<purpose>: core cannot be asked for any other, and the
// server secret itself is never exposed. Changing the server secret makes
// existing blobs unreadable, like every other sealed value in core.
package secret
