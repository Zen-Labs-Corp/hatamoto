package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// UnpublishWorker takes published entities offline once their scheduled
// unpublish_at falls due. Like PublishWorker it holds primitives only: the
// transition itself (status change, cache, redirect, audit) is the sink's, so
// it goes through the same service path as an owner clicking Unpublish.
type UnpublishWorker struct {
	db     *database.DB
	logger *slog.Logger
	sink   UnpublishSink
}

// UnpublishSink performs one unpublish. It must be idempotent: an entity that
// is no longer published is skipped without error, so a second replica or a
// retried tick does nothing.
type UnpublishSink interface {
	Unpublish(ctx context.Context, id uuid.UUID) error
}

// NewUnpublishWorker constructs the worker.
func NewUnpublishWorker(db *database.DB, logger *slog.Logger, sink UnpublishSink) *UnpublishWorker {
	return &UnpublishWorker{db: db, logger: logger, sink: sink}
}

// Run executes one tick and returns how many entities it handled. A failing
// entity is logged and left due, to be retried on the next tick.
func (w *UnpublishWorker) Run(ctx context.Context) (int, error) {
	adminCtx := database.WithSuperadmin(ctx)
	pool := w.db.Pool()

	rows, err := pool.Query(adminCtx, `
		SELECT entity_id FROM hatamoto_entity_schedule
		 WHERE unpublish_at <= NOW()
		 ORDER BY unpublish_at
		 LIMIT 200`)
	if err != nil {
		return 0, fmt.Errorf("select due unpublishes: %w", err)
	}
	var due []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan due unpublish: %w", err)
		}
		due = append(due, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate due unpublishes: %w", err)
	}

	done := 0
	for _, id := range due {
		if err := w.sink.Unpublish(ctx, id); err != nil {
			w.logger.Error("scheduled unpublish failed", slog.String("entity_id", id.String()), slog.Any("error", err))
			continue
		}
		if _, err := pool.Exec(adminCtx,
			`DELETE FROM hatamoto_entity_schedule WHERE entity_id = $1`, id); err != nil {
			return done, fmt.Errorf("drop finished unpublish: %w", err)
		}
		done++
	}
	return done, nil
}
