package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"math"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

// CommentReader is what the comments host functions need. Implementations
// return approved comments of public entities only, and no email, hash, IP or
// user agent.
type CommentReader interface {
	Thread(ctx context.Context, entityID uuid.UUID, page int, opts ThreadOpts) (sdk.CommentThread, error)
	Counts(ctx context.Context, entityIDs []uuid.UUID) (map[uuid.UUID]int, error)
}

// ErrCommentNotFound is a CommentReader's answer for a comment a plugin may not
// read: unknown, pending/spam/trash/draft, under such an ancestor or on a
// non-public entity (a dead comment is not one of them: it is a placeholder).
// The host function maps it to not_found "comment not found".
var ErrCommentNotFound = errors.New("comment not found")

// ThreadOpts is the caller's identity and what it asked for. IncludeDead is
// already gated on comments.moderate; the reader still decides ownership from
// the content type's origin plugin at call time and never trusts Schema alone.
type ThreadOpts struct {
	Plugin, Schema string
	IncludeDead    bool
}

const (
	maxCommentCountIDs = 100
	maxCommentPage     = 10_000
)

func fnCommentsThread(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
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
	// include_dead is honoured only with comments.moderate.
	opts := ThreadOpts{
		Plugin: e.plugin, Schema: e.manifest.EntitySchema,
		IncludeDead: args.IncludeDead && a.grants.Has(e.plugin, sdk.PermCommentsModerate),
	}
	return a.svc.Comments.Thread(ctx, id, clamp(args.Page, 1, maxCommentPage), opts)
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

// CommentModerator is the comment write side a plugin with comments.moderate
// reaches (scope = its manifest name + entity_schema; the implementation
// decides ownership from the schema's origin_plugin at call time, never from
// the manifest alone).
type CommentModerator interface {
	MarkDead(ctx context.Context, plugin, schema string, ids []uuid.UUID) (int, error)
	Restore(ctx context.Context, plugin, schema string, ids []uuid.UUID) (int, error)
	SetRank(ctx context.Context, plugin, schema string, items []RankItem) (int, error)
	SetViewer(ctx context.Context, plugin, commenterKey string, showDead bool) error
}

// RankItem is one comment's new rank.
type RankItem struct {
	ID   uuid.UUID
	Rank float64
}

// ErrCommentNotOwned means a comment is not on a content type the plugin owns.
var ErrCommentNotOwned = errors.New("comment is not on the plugin's content type")

const maxCommenterKeyBytes = 128

// moderationGate runs the checks every moderation call shares once its
// arguments are valid: schema, service, then one limiter token.
func moderationGate(a *API, e *callEnv, fn string) error {
	if e.manifest.EntitySchema == "" {
		return herr(sdk.ErrNotOwned, "the manifest declares no entity_schema")
	}
	if a.svc.CommentModerator == nil {
		return unavailable(fn)
	}
	return a.allowWrite(e.plugin, fn)
}

func (a *API) auditModeration(ctx context.Context, plugin string, detail map[string]any) {
	if a.svc.Audit != nil {
		a.svc.Audit(ctx, "plugin.comment.moderate", "plugin:"+plugin, detail)
	}
}

// parseUniqueIDs parses 1..max distinct comment ids.
func parseUniqueIDs(raw []string, max int) ([]uuid.UUID, error) {
	if n := len(raw); n == 0 || n > max {
		return nil, invalid("ids needs 1 to %d ids", max)
	}
	seen := make(map[uuid.UUID]bool, len(raw))
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := parseID(s)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, invalid("ids must be unique")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func fnCommentsMarkDead(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	return commentsByIDs(ctx, a, e, raw, sdk.FnCommentsMarkDead, func(m CommentModerator, ids []uuid.UUID) (int, error) {
		return m.MarkDead(ctx, e.plugin, e.manifest.EntitySchema, ids)
	})
}

func fnCommentsRestore(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	return commentsByIDs(ctx, a, e, raw, sdk.FnCommentsRestore, func(m CommentModerator, ids []uuid.UUID) (int, error) {
		return m.Restore(ctx, e.plugin, e.manifest.EntitySchema, ids)
	})
}

func commentsByIDs(ctx context.Context, a *API, e *callEnv, raw jsontext.Value, fn string,
	do func(CommentModerator, []uuid.UUID) (int, error),
) (any, error) {
	args, err := decodeArgs[sdk.CommentIDsArgs](raw)
	if err != nil {
		return nil, err
	}
	ids, err := parseUniqueIDs(args.IDs, sdk.MaxCommentIDs)
	if err != nil {
		return nil, err
	}
	if err := moderationGate(a, e, fn); err != nil {
		return nil, err
	}
	n, err := do(a.svc.CommentModerator, ids)
	if err != nil {
		return nil, err
	}
	a.auditModeration(ctx, e.plugin, map[string]any{"fn": fn, "requested": len(ids), "changed": n, "ids": args.IDs})
	return sdk.CommentsChanged{Changed: n}, nil
}

func fnCommentsSetRank(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	args, err := decodeArgs[sdk.CommentRankArgs](raw)
	if err != nil {
		return nil, err
	}
	if n := len(args.Items); n == 0 || n > sdk.MaxCommentRankItems {
		return nil, invalid("items needs 1 to %d entries", sdk.MaxCommentRankItems)
	}
	seen := make(map[uuid.UUID]bool, len(args.Items))
	items := make([]RankItem, 0, len(args.Items))
	for _, it := range args.Items {
		id, err := parseID(it.ID)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(it.Rank) || math.IsInf(it.Rank, 0) {
			return nil, invalid("rank must be a finite number")
		}
		if seen[id] {
			return nil, invalid("item ids must be unique")
		}
		seen[id] = true
		items = append(items, RankItem{ID: id, Rank: it.Rank})
	}
	if err := moderationGate(a, e, sdk.FnCommentsSetRank); err != nil {
		return nil, err
	}
	n, err := a.svc.CommentModerator.SetRank(ctx, e.plugin, e.manifest.EntitySchema, items)
	if err != nil {
		return nil, err
	}
	a.auditModeration(ctx, e.plugin, map[string]any{"fn": sdk.FnCommentsSetRank, "requested": len(items), "changed": n})
	return sdk.CommentsChanged{Changed: n}, nil
}

func fnCommentsSetViewer(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	args, err := decodeArgs[sdk.CommentViewerArgs](raw)
	if err != nil {
		return nil, err
	}
	if n := len(args.CommenterKey); n == 0 || n > maxCommenterKeyBytes {
		return nil, invalid("commenter_key needs 1 to %d bytes", maxCommenterKeyBytes)
	}
	if err := moderationGate(a, e, sdk.FnCommentsSetViewer); err != nil {
		return nil, err
	}
	if err := a.svc.CommentModerator.SetViewer(ctx, e.plugin, args.CommenterKey, args.ShowDead); err != nil {
		return nil, err
	}
	a.auditModeration(ctx, e.plugin, map[string]any{"fn": sdk.FnCommentsSetViewer, "show_dead": args.ShowDead})
	return struct{}{}, nil
}
