package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/jsonx"
)

// adminAudit writes one hatamoto_admin_audit row (and a log line) for an
// owner action. The write outlives a cancelled request; it is skipped when
// there is no database (route-table builds, tests).
func (s *Server) adminAudit(ctx context.Context, e admin.AuditEvent) {
	raw, _ := jsonx.Marshal(e.Detail)
	if s.db != nil {
		err := auditlog.Recorder{Pool: s.db.Pool()}.Record(ctx, nil, auditEventFor(e))
		if err != nil {
			s.logger.Error("admin audit write failed", "action", e.Action, "error", err)
		}
	}
	attrs := []any{"action", e.Action, "actor", e.Actor, "target", e.Target, "detail", string(raw)}
	if e.Result != "" {
		attrs = append(attrs, "result", e.Result)
	}
	s.logger.Info("admin audit", attrs...)
}

// auditEventFor maps an admin event to the stored event. Unset ActorKind and
// Result keep the kind derived from the actor and the default result.
func auditEventFor(e admin.AuditEvent) audit.Event {
	kind := e.ActorKind
	if kind == "" {
		kind = auditlog.KindFor(e.ActorID, e.Actor)
	}
	return audit.Event{
		ActorID: e.ActorID, Actor: e.Actor, ActorKind: kind, KeyID: e.KeyID,
		Action: e.Action, Target: e.Target, Detail: e.Detail, Result: e.Result,
		IP: e.IP, UserAgent: e.UserAgent, RequestID: e.RequestID,
	}
}

// adminAuditLog reads hatamoto_admin_audit back for the Audit log page and the
// Overview Activity card.
type adminAuditLog struct{ pool *pgxpool.Pool }

// Recent lists events newest first and the total number of stored events.
func (l adminAuditLog) Recent(ctx context.Context, limit, offset int) ([]admin.AuditRow, int, error) {
	var total int
	if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit rows: %w", err)
	}
	out, err := l.list(ctx, limit, offset)
	return out, total, err
}

// Latest lists the newest events without counting the table.
func (l adminAuditLog) Latest(ctx context.Context, limit int) ([]admin.AuditRow, error) {
	return l.list(ctx, limit, 0)
}

func (l adminAuditLog) list(ctx context.Context, limit, offset int) ([]admin.AuditRow, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT id, at, coalesce(actor,''), action, coalesce(target,''), coalesce(detail,'{}'::jsonb)
		   FROM hatamoto_admin_audit ORDER BY at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit rows: %w", err)
	}
	defer rows.Close()
	var out []admin.AuditRow
	for rows.Next() {
		var (
			r   admin.AuditRow
			at  time.Time
			raw []byte
		)
		if err := rows.Scan(&r.ID, &at, &r.Actor, &r.Action, &r.Target, &raw); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		r.At = at
		_ = jsonx.Unmarshal(raw, &r.Detail, jsonx.Stored) // a malformed detail shows as empty
		out = append(out, r)
	}
	return out, rows.Err()
}
