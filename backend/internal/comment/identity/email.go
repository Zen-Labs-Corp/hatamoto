package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"hatamoto.dev/core/internal/comment"
)

var (
	// ErrEmailInvalid means the address is not a plain, single address.
	ErrEmailInvalid = errors.New("identity: email invalid")
	// ErrEmailNotAccepted means the address's domain is on the blocklist.
	ErrEmailNotAccepted = errors.New("identity: email not accepted")
)

// Messages for the two refusals. The second does not say why, so a spammer
// learns nothing about the list.
const (
	MsgEmailInvalid     = "Please enter a valid email address."
	MsgEmailNotAccepted = "Please use a different email address."
)

const maxEmailLen = 254

// blockMatcher is the slice of *comment.Service that CheckEmail needs.
type blockMatcher interface {
	MatchBlocks(ctx context.Context, p comment.Probe) ([]comment.Block, error)
}

// checkEmail validates raw and refuses domains on the model's email-domain
// blocklist (disposable providers are seeded there; the model matches parent
// domains). A lookup failure is returned as is, not as a refusal: the caller
// treats it as temporary.
func checkEmail(ctx context.Context, bl blockMatcher, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxEmailLen || strings.ContainsAny(raw, "\r\n") {
		return ErrEmailInvalid
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw || strings.Count(raw, "@") != 1 {
		return ErrEmailInvalid
	}
	local, domain, _ := strings.Cut(raw, "@")
	if local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return ErrEmailInvalid
	}
	blocks, err := bl.MatchBlocks(ctx, comment.Probe{EmailDomain: comment.EmailDomain(raw)})
	if err != nil {
		return fmt.Errorf("identity: blocklist: %w", err)
	}
	for _, b := range blocks {
		if b.Kind == comment.BlockEmailDomain {
			return ErrEmailNotAccepted
		}
	}
	return nil
}
