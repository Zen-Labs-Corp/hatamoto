// Package auditlog writes audit events: the default hatamoto_admin_audit row
// and then the registered sink. The public hatamoto.dev/core/audit package
// wraps it for transactions; core's writers without a transaction use a
// Recorder bound to their own pool. It must not import the public package.
package auditlog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/jsonx"
)

// Shared with the public package through auditstate.
var (
	ErrTable = auditstate.ErrTable
	ErrSink  = auditstate.ErrSink
)

const (
	maxUserAgent = 512
	maxRequestID = 128
)

const insertSQL = `INSERT INTO hatamoto_admin_audit
	(actor_id, actor, action, target, detail, actor_kind, actor_key_id, ip, user_agent, request_id, result)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

// Recorder writes audit events. Pool may be nil when every call passes a
// non-nil tx.
type Recorder struct{ Pool *pgxpool.Pool }

// Record redacts ev, writes the default table row (through tx under a
// savepoint when tx is non-nil, else through Pool without the caller's
// cancellation) and then calls the registered sink synchronously with the
// same tx (which may be nil). It returns nil, an error wrapping ErrTable
// (the row failed; the caller keeps its own handling) or an error wrapping
// ErrSink (the registered sink failed; the caller must fail the write).
// With neither tx nor Pool the table write is skipped.
func (r Recorder) Record(ctx context.Context, tx pgx.Tx, ev auditstate.Event) error {
	ev.Detail = Redact(ev.Detail)
	ev = clamp(ev)
	raw, _ := jsonx.Marshal(ev.Detail)

	var terr error
	switch {
	case tx != nil:
		terr = insertSavepoint(ctx, tx, ev, raw)
	case r.Pool != nil:
		_, terr = r.Pool.Exec(context.WithoutCancel(ctx), insertSQL, args(ev, raw)...)
	}
	if s := auditstate.Registered(); s != nil {
		if err := s.Record(ctx, tx, ev); err != nil {
			return fmt.Errorf("%w: %w", ErrSink, err)
		}
	}
	if terr != nil {
		return fmt.Errorf("%w: %w", ErrTable, terr)
	}
	return nil
}

// insertSavepoint inserts inside a savepoint so a failed statement never
// aborts the business transaction.
func insertSavepoint(ctx context.Context, tx pgx.Tx, ev auditstate.Event, raw []byte) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err := sp.Exec(ctx, insertSQL, args(ev, raw)...); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}

func args(ev auditstate.Event, raw []byte) []any {
	return []any{parseUUID(ev.ActorID), ev.Actor, ev.Action, ev.Target, raw,
		ev.ActorKind, parseUUID(ev.KeyID), ev.IP, ev.UserAgent, ev.RequestID, ev.Result}
}

// parseUUID returns nil (SQL NULL) for an empty or invalid id.
func parseUUID(s string) *uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

func clamp(ev auditstate.Event) auditstate.Event {
	ev.UserAgent = truncate(ev.UserAgent, maxUserAgent)
	ev.RequestID = truncate(ev.RequestID, maxRequestID)
	if ev.Result == "" {
		ev.Result = "ok"
	}
	return ev
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// MustFail reports whether err means the caller must fail the write.
func MustFail(err error) bool { return errors.Is(err, ErrSink) }

// KindFor derives the actor kind from the actor strings core stores today.
func KindFor(actorID, actor string) string {
	switch {
	case actor == "cli":
		return "cli"
	case actor == "api":
		return "api_key"
	}
	if _, err := uuid.Parse(actorID); err == nil {
		return "user"
	}
	return "system"
}
