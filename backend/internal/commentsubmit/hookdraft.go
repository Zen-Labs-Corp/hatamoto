package commentsubmit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

// DefaultDraftTimeout bounds one comment.draft chain.
const DefaultDraftTimeout = 500 * time.Millisecond

// ErrDraftUnavailable means no name could be had from the comment.draft chain
// (no filter, failure, timeout, a blank or foreign result). The submit is refused.
var ErrDraftUnavailable = errors.New("commentsubmit: sign-in name unavailable")

// DraftRejected is a plugin's public refusal (the reject field of
// sdk.CommentDraft), already cleaned and cut to sdk.CommentDraftMaxReject bytes
// by pluginhost.
type DraftRejected struct{ Message string }

// Error never carries the message, so it cannot reach a log line.
func (e *DraftRejected) Error() string { return "commentsubmit: draft rejected" }

// HookDrafter asks the comment.draft filter for the locked name of a signed-in
// commenter. Fail closed, the opposite of HookChecker: no filter, no name.
type HookDrafter struct {
	Engine  filterEngine
	Timeout time.Duration
	Log     *slog.Logger
}

// Name runs the chain for commenter on a policy schema. The contract sends
// author_name empty; the plugin key arrives per plugin from the ctx carrier.
func (h HookDrafter) Name(ctx context.Context, commenter uuid.UUID, schema string) (name string, err error) {
	defer func() {
		if recover() != nil {
			name, err = "", ErrDraftUnavailable
		}
	}()
	if h.Engine == nil || h.Engine.FilterCount(sdk.CommentDraftF.Name) == 0 {
		return "", ErrDraftUnavailable
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultDraftTimeout
	}
	ctx, cancel := context.WithTimeout(pluginkey.WithCommenter(ctx, commenter), timeout)
	defer cancel()
	out, err := h.Engine.ApplyClosed(ctx, sdk.CommentDraftF.Name, sdk.CommentDraft{Schema: schema})
	if err != nil || ctx.Err() != nil {
		var rej *sdk.Reject
		if err != nil && ctx.Err() == nil && errors.As(err, &rej) && rej.Reason != "" {
			return "", &DraftRejected{Message: rej.Reason}
		}
		h.log().DebugContext(ctx, "comment.draft unavailable")
		return "", ErrDraftUnavailable
	}
	res, ok := out.(sdk.CommentDraft)
	if !ok {
		return "", ErrDraftUnavailable
	}
	name = strings.TrimSpace(res.AuthorName)
	if name == "" {
		return "", ErrDraftUnavailable
	}
	if r := []rune(name); len(r) > MaxNameRunes {
		name = strings.TrimSpace(string(r[:MaxNameRunes]))
	}
	return name, nil
}

func (h HookDrafter) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}
