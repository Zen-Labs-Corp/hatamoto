package server

import (
	"context"
	"testing"
)

func TestPluginAuditNilDBSafe(t *testing.T) {
	t.Parallel()
	s := &Server{logger: quietLogger()}
	s.pluginAudit(context.Background(), "fixture", "plugin.entity.create", "id", map[string]any{"plugin": "fixture"})
}
