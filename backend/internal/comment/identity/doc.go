// Package identity confirms a commenter's email once per device: the
// single-use confirmation link, the hm_c device cookie, the send limits and
// the hourly purge of unconfirmed drafts. Trust lives in hatamoto_commenters,
// never in the cookie.
package identity
