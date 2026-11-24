package server

import (
	"context"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
)

// pluginAudit writes the audit row of a plugin's successful entity write: the
// actor is the plugin, the target the entry. The host calls it only after the
// write succeeded. A failed row is logged and never undoes the write.
func (s *Server) pluginAudit(ctx context.Context, plugin, action, target string, detail map[string]any) {
	if s.db == nil {
		return
	}
	err := auditlog.Recorder{Pool: s.db.Pool()}.Record(ctx, nil, auditstate.Event{
		Actor: "plugin:" + plugin, ActorKind: audit.KindPlugin,
		Action: action, Target: target, Detail: detail,
	})
	if err != nil {
		s.logger.Error("plugin audit write failed", "action", action, "error", err)
	}
}
