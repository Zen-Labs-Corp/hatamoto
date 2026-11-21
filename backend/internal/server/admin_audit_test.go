package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/admin"
)

func TestAdminAuditWithoutDBIsNoop(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", ActorID: "not-a-uuid"})
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", Detail: map[string]any{"pages": 3}})
	s.adminAudit(context.Background(), admin.AuditEvent{Action: "cache.clear", IP: "203.0.113.9", UserAgent: "ua", RequestID: "rid"})
}

// A reader that cannot get a connection in time returns an error instead of
// waiting for the caller's context.
func TestAdminAuditLogQueriesTimeOut(t *testing.T) {
	old := auditQueryTimeout
	auditQueryTimeout = 50 * time.Millisecond
	t.Cleanup(func() { auditQueryTimeout = old })

	pool, err := pgxpool.New(context.Background(), "postgres://u:p@192.0.2.1:5432/db?connect_timeout=10")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := adminAuditLog{pool: pool}

	start := time.Now()
	if _, err := log.Search(context.Background(), admin.AuditQuery{Q: "x"}); err == nil {
		t.Error("Search without a database returned no error")
	}
	if _, err := log.Facets(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Error("Facets without a database returned no error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("queries took %v, want them cut off by the timeout", d)
	}
}
