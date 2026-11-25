package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginhost"
)

// pluginCommentIdentity serves comments.identity for the plugin host. It reads
// s.comments at call time: the comment service is built after the plugins. The
// raw account is never logged or audited; only the commenter id is.
type pluginCommentIdentity struct {
	s      *Server
	derive func(plugin string, id uuid.UUID) string
}

func (p pluginCommentIdentity) Ensure(ctx context.Context, plugin, account string) (string, error) {
	if p.s.comments == nil || p.derive == nil {
		return "", pluginhost.ErrCommentIdentityUnavailable
	}
	id, created, err := p.s.comments.EnsurePluginCommenter(ctx, plugin, account)
	if err != nil {
		return "", err
	}
	if created {
		p.s.pluginModerationAudit(ctx, "plugin.comment.identity", "plugin:"+plugin, map[string]any{"fn": "ensure", "commenter": id.String()})
	}
	return p.derive(plugin, id), nil
}

func (p pluginCommentIdentity) Revoke(ctx context.Context, plugin, account string) (bool, error) {
	if p.s.comments == nil || p.derive == nil {
		return false, pluginhost.ErrCommentIdentityUnavailable
	}
	id, ok, err := p.s.comments.PluginCommenter(ctx, plugin, account)
	if err != nil || !ok {
		return false, err
	}
	if _, err := p.s.comments.RevokeDevices(ctx, id); err != nil {
		return false, err
	}
	p.s.pluginModerationAudit(ctx, "plugin.comment.identity", "plugin:"+plugin, map[string]any{"fn": "revoke", "commenter": id.String()})
	return true, nil
}
