package server

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// pluginCommentModerator implements pluginhost.CommentModerator over the
// comment service, read at call time (plugins are set up before comments). It
// never decides ownership: the service reads the content type's origin_plugin
// inside each write.
type pluginCommentModerator struct{ s *Server }

func (m pluginCommentModerator) svc() (*comment.Service, error) {
	if m.s.comments == nil {
		return nil, &sdk.HostError{Code: sdk.ErrUnavailable, Message: "comments are not available"}
	}
	return m.s.comments, nil
}

func notOwned(err error) error {
	if errors.Is(err, comment.ErrNotOwned) {
		return pluginhost.ErrCommentNotOwned
	}
	return err
}

func (m pluginCommentModerator) MarkDead(ctx context.Context, plugin, schema string, ids []uuid.UUID) (int, error) {
	svc, err := m.svc()
	if err != nil {
		return 0, err
	}
	n, err := svc.MarkDead(ctx, comment.Scope{Plugin: plugin, Schema: schema}, ids)
	return n, notOwned(err)
}

func (m pluginCommentModerator) Restore(ctx context.Context, plugin, schema string, ids []uuid.UUID) (int, error) {
	svc, err := m.svc()
	if err != nil {
		return 0, err
	}
	n, err := svc.Restore(ctx, comment.Scope{Plugin: plugin, Schema: schema}, ids)
	return n, notOwned(err)
}

func (m pluginCommentModerator) SetRank(ctx context.Context, plugin, schema string, items []pluginhost.RankItem) (int, error) {
	svc, err := m.svc()
	if err != nil {
		return 0, err
	}
	ranks := make([]comment.RankItem, len(items))
	for i, it := range items {
		ranks[i] = comment.RankItem{ID: it.ID, Rank: it.Rank}
	}
	n, err := svc.SetRank(ctx, comment.Scope{Plugin: plugin, Schema: schema}, ranks)
	return n, notOwned(err)
}

func (m pluginCommentModerator) SetViewer(ctx context.Context, plugin, commenterKey string, showDead bool) error {
	svc, err := m.svc()
	if err != nil {
		return err
	}
	return svc.SetViewerFlag(ctx, plugin, commenterKey, showDead)
}

// pluginModerationAudit records a plugin's moderation write. No request exists
// for a host call: the actor is the plugin (target "plugin:<name>"), never a
// user, and the detail carries counts and ids only.
func (s *Server) pluginModerationAudit(ctx context.Context, action, target string, detail map[string]any) {
	s.adminAudit(ctx, admin.AuditEvent{Actor: target, ActorKind: audit.KindSystem, Action: action, Target: target, Detail: detail})
}
