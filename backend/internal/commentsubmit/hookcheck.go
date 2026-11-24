package commentsubmit

import (
	"context"
	"errors"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/sdk"
)

// HookName is the filter plugins attach to (sdk.CommentCheckF).
const HookName = "comment.check"

// ErrHookHold is returned when the comment.check chain cannot give a verdict;
// comment.RunCheckers turns any error into a hold.
var ErrHookHold = errors.New("commentsubmit: comment.check unavailable")

// CheckPayload is the value of the comment.check chain: the public SDK type, so
// what a plugin sees is the documented wire shape. Body, link domains and the
// body hash only: never the address, a network hash, an email, a name or the
// entity. addr_key and area_key are per-plugin keys the plugin host adds, only
// for plugins holding client.key.
type CheckPayload = sdk.CommentCheck

type filterEngine interface {
	FilterCount(string) int
	ApplyClosed(context.Context, string, any) (any, error)
}

// HookChecker adapts the comment.check filter to comment.Checker. The timeout
// belongs to comment.RunCheckers. Fail closed: a filter that errors, panics,
// times out or returns the wrong type holds the comment.
type HookChecker struct{ Engine filterEngine }

var _ comment.Checker = HookChecker{}

// Check runs the filter chain. Without filters it adds nothing. A failed or
// cancelled chain, or a result of another type, is ErrHookHold.
func (h HookChecker) Check(ctx context.Context, in comment.CheckInput) (score int, reasons []string, err error) {
	defer func() {
		if recover() != nil {
			score, reasons, err = 0, nil, ErrHookHold
		}
	}()
	if h.Engine == nil || h.Engine.FilterCount(HookName) == 0 {
		return 0, nil, nil
	}
	out, err := h.Engine.ApplyClosed(ctx, HookName, CheckPayload{
		Schema:      in.Schema,
		Body:        in.Body,
		LinkDomains: in.LinkDomains,
		BodyHash:    in.BodyHash,
	})
	if err != nil || ctx.Err() != nil {
		return 0, nil, ErrHookHold
	}
	res, ok := out.(CheckPayload)
	if !ok {
		return 0, nil, ErrHookHold
	}
	return commentCheckPoints(res.Score), res.Reasons, nil
}

// commentCheckPoints maps the plugins' total (0..sdk.CommentCheckMaxScore, cut)
// onto the spam scale, rounding half up: the maximum is spam.SpamScore points.
func commentCheckPoints(s int) int {
	s = min(max(s, 0), sdk.CommentCheckMaxScore)
	return (s*spam.SpamScore + sdk.CommentCheckMaxScore/2) / sdk.CommentCheckMaxScore
}
