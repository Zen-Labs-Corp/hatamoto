package commentsubmit

import (
	"context"
	"errors"

	"hatamoto.dev/core/internal/comment"
)

// HookName is the filter plugins attach to.
const HookName = "comment.check"

// ErrHookHold is returned when the comment.check chain cannot give a verdict;
// comment.RunCheckers turns any error into a hold.
var ErrHookHold = errors.New("commentsubmit: comment.check unavailable")

// CheckPayload is passed to the filters, which return the same type. Score is
// additive points. It carries the body, its link domains and its hash only:
// never an IP, a network hash, an email or a name.
type CheckPayload struct {
	Body        string
	LinkDomains []string
	BodyHash    string
	Score       int
	Reasons     []string
}

type filterEngine interface {
	FilterCount(string) int
	ApplyStrict(context.Context, string, any) (any, error)
}

// HookChecker adapts the comment.check filter to comment.Checker. The timeout
// belongs to comment.RunCheckers.
type HookChecker struct{ Engine filterEngine }

var _ comment.Checker = HookChecker{}

// Check runs the filter chain. Without filters it adds nothing. A failed or
// cancelled chain, or a result of another type, is ErrHookHold.
func (h HookChecker) Check(ctx context.Context, in comment.CheckInput) (int, []string, error) {
	if h.Engine == nil || h.Engine.FilterCount(HookName) == 0 {
		return 0, nil, nil
	}
	out, err := h.Engine.ApplyStrict(ctx, HookName, CheckPayload{
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
	return res.Score, res.Reasons, nil
}
