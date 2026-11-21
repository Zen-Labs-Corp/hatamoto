package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditlog"
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

func TestAdminAuditEventForKeepsTodaysRow(t *testing.T) {
	t.Parallel()

	plain := auditEventFor(admin.AuditEvent{ActorID: "u1", Actor: "a@b.c", Action: "cache.clear"})
	if plain.ActorKind != auditlog.KindFor("u1", "a@b.c") || plain.Result != "" || plain.KeyID != "" {
		t.Fatalf("zero values changed the row: %+v", plain)
	}

	signIn := auditEventFor(admin.AuditEvent{
		Actor: "email-hash:x", Action: "session.sign_in_failed",
		ActorKind: audit.KindUser, Result: audit.ResultDenied, KeyID: "k1",
	})
	if signIn.ActorKind != audit.KindUser || signIn.Result != audit.ResultDenied || signIn.KeyID != "k1" {
		t.Fatalf("explicit fields lost: %+v", signIn)
	}
}
