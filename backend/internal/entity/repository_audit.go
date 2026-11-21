package entity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
)

// Audit action names (labels live in the admin audit log view).
const (
	auditCreate    = "entity.create"
	auditUpdate    = "entity.update"
	auditPublish   = "entity.publish"
	auditUnpublish = "entity.unpublish"
	auditArchive   = "entity.archive"
	auditSchedule  = "entity.schedule"
	auditDelete    = "entity.delete"
)

// TxAudit records ev inside tx. Only an error that must fail the write
// (a registered sink's) is returned; the server wires audit.Record.
type TxAudit func(ctx context.Context, tx pgx.Tx, ev auditstate.Event) error

// SetAuditTx turns on in-transaction audit for writes whose ctx carries an
// auditctx.Actor (API and MCP). Without it, or without an actor, writes are as before.
func (r *PostgresEntityRepository) SetAuditTx(f TxAudit) { r.auditTx = f }

func (r *PostgresEntityRepository) auditActor(ctx context.Context) (auditctx.Actor, bool) {
	if r.auditTx == nil {
		return auditctx.Actor{}, false
	}
	return auditctx.From(ctx)
}

// auditEvent builds the event for action on entity id; the actor's extra
// detail fills keys the caller did not set.
func auditEvent(a auditctx.Actor, action string, id uuid.UUID, detail map[string]any) auditstate.Event {
	for k, v := range a.Detail {
		if _, set := detail[k]; !set {
			detail[k] = v
		}
	}
	if a.Via != "" { // the admin passes "" so its rows keep today's shape
		detail["via"] = a.Via
	}
	return auditstate.Event{
		ActorID: a.ID, Actor: a.Name, ActorKind: a.Kind, KeyID: a.KeyID,
		Action: action, Target: id.String(), Detail: detail,
		IP: a.IP, UserAgent: a.UserAgent, RequestID: a.RequestID,
	}
}

func (r *PostgresEntityRepository) recordTx(ctx context.Context, tx pgx.Tx, a auditctx.Actor, action string, id uuid.UUID, detail map[string]any) error {
	return r.auditTx(ctx, tx, auditEvent(a, action, id, detail))
}

// auditStatusAction names the audit action of a move to st: published is a
// publish, archived an archive, anything else (draft) an unpublish.
func auditStatusAction(st EntityStatus) string {
	switch st {
	case StatusPublished:
		return auditPublish
	case StatusArchived:
		return auditArchive
	}
	return auditUnpublish
}

// rowQuerier is what a single-statement write needs; both a connection and a
// transaction satisfy it.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// auditedWrite finishes a single-statement write: it commits the transaction
// opened by auditedQuerier after recording the audit row, or does nothing when
// the write is not audited.
type auditedWrite struct {
	r     *PostgresEntityRepository
	tx    pgx.Tx
	actor auditctx.Actor
}

func (w auditedWrite) audited() bool { return w.tx != nil }

func (w auditedWrite) rollback() {
	if w.tx != nil {
		_ = w.tx.Rollback(context.Background()) // no-op after commit
	}
}

func (w auditedWrite) commit(ctx context.Context, action string, id uuid.UUID, detail map[string]any) error {
	if w.tx == nil {
		return nil
	}
	if err := w.r.recordTx(ctx, w.tx, w.actor, action, id, detail); err != nil {
		return err
	}
	if err := w.tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// auditedQuerier returns conn itself for an unaudited write (no extra round
// trips) and a fresh transaction when ctx carries an actor and the hook is set.
func (r *PostgresEntityRepository) auditedQuerier(ctx context.Context, conn *pgxpool.Conn) (rowQuerier, auditedWrite, error) {
	a, ok := r.auditActor(ctx)
	if !ok {
		return conn, auditedWrite{}, nil
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, auditedWrite{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return tx, auditedWrite{r: r, tx: tx, actor: a}, nil
}
