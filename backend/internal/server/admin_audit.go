package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/admin"
)

// adminAudit writes one hatamoto_admin_audit row (and a log line) for an
// owner action. The write outlives a cancelled request; it is skipped when
// there is no database (route-table builds, tests).
func (s *Server) adminAudit(ctx context.Context, e admin.AuditEvent) {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	if s.db != nil {
		var actorID *uuid.UUID
		if id, err := uuid.Parse(e.ActorID); err == nil {
			actorID = &id
		}
		if _, err := s.db.Pool().Exec(context.WithoutCancel(ctx),
			`INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES ($1, $2, $3, $4, $5)`,
			actorID, e.Actor, e.Action, e.Target, raw); err != nil {
			s.logger.Error("admin audit write failed", "action", e.Action, "error", err)
		}
	}
	s.logger.Info("admin audit", "action", e.Action, "actor", e.Actor, "target", e.Target, "detail", string(raw))
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
		_ = json.Unmarshal(raw, &r.Detail) // a malformed detail shows as empty
		out = append(out, r)
	}
	return out, rows.Err()
}
