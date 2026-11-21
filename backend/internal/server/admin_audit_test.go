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
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", IP: "203.0.113.9", UserAgent: "ua", RequestID: "rid"})
}

func TestAuditEventForKeepsResult(t *testing.T) {
	for _, res := range []string{"", "ok", "denied", "error"} {
		if got := auditEventFor(admin.AuditEvent{Action: "session.sign_in_failed", Result: res}).Result; got != res {
			t.Errorf("Result %q became %q", res, got)
		}
	}
}
