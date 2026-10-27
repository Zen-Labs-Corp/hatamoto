package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/admin"
)

func TestAdminAuditWithoutDBIsNoop(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", ActorID: "not-a-uuid"})
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", Detail: map[string]any{"pages": 3}})
}
