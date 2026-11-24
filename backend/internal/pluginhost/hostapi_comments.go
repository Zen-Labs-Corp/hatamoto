package pluginhost

import (
	"context"
	"encoding/json/jsontext"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

// CommentReader is what the comments host functions need. Implementations
// return approved comments of public entities only, and no email, hash, IP or
// user agent.
type CommentReader interface {
	Thread(ctx context.Context, entityID uuid.UUID, page int) (sdk.CommentThread, error)
	Counts(ctx context.Context, entityIDs []uuid.UUID) (map[uuid.UUID]int, error)
}

const (
	maxCommentCountIDs = 100
	maxCommentPage     = 10_000
)

func fnCommentsThread(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	// include_dead is accepted and ignored: it is honoured with comments.moderate
	// once dead comments exist; until then no comment is dead.
	args, err := decodeArgs[sdk.CommentsThreadArgs](raw)
	if err != nil {
		return nil, err
	}
	id, err := parseID(args.EntityID)
	if err != nil {
		return nil, err
	}
	if a.svc.Comments == nil {
		return nil, unavailable("comments")
	}
	return a.svc.Comments.Thread(ctx, id, clamp(args.Page, 1, maxCommentPage))
}

func fnCommentsCounts(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	args, err := decodeArgs[struct {
		EntityIDs []string `json:"entity_ids"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if n := len(args.EntityIDs); n == 0 || n > maxCommentCountIDs {
		return nil, invalid("entity_ids needs 1 to %d ids", maxCommentCountIDs)
	}
	ids := make([]uuid.UUID, 0, len(args.EntityIDs))
	for _, s := range args.EntityIDs {
		id, err := parseID(s)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if a.svc.Comments == nil {
		return nil, unavailable("comments")
	}
	got, err := a.svc.Comments.Counts(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(got))
	for id, n := range got {
		out[id.String()] = n
	}
	return sdk.CommentCounts{Counts: out}, nil
}
