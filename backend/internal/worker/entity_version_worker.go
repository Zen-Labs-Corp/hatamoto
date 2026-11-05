package worker

import (
	"context"
	"fmt"
	"log/slog"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// EntityVersionWorker coalesces identical-consecutive entity versions and keeps
// the newest N per entity (never a version live in the last 30 days), so hatamoto_entity_versions stays bounded.
type EntityVersionWorker struct {
	repo   entity.EntityRepository
	keep   func(context.Context) (int, error)
	logger *slog.Logger
}

// NewEntityVersionWorker prunes to the number keep returns on each run (the
// owner setting), clamped to the allowed range. A keep error skips the run, so
// an unreadable setting never deletes history. A nil keep means
// entity.VersionKeepDefault.
func NewEntityVersionWorker(repo entity.EntityRepository, keep func(context.Context) (int, error), logger *slog.Logger) *EntityVersionWorker {
	return &EntityVersionWorker{repo: repo, keep: keep, logger: logger}
}

func (w *EntityVersionWorker) keepNow(ctx context.Context) (int, error) {
	if w.keep == nil {
		return entity.VersionKeepDefault, nil
	}
	n, err := w.keep(ctx)
	if err != nil {
		return 0, err
	}
	return min(max(n, entity.VersionKeepMin), entity.VersionKeepMax), nil
}

func (w *EntityVersionWorker) Run(ctx context.Context) error {
	if w.repo == nil {
		return nil
	}
	keep, err := w.keepNow(ctx)
	if err != nil {
		return fmt.Errorf("read version history limit: %w", err)
	}
	n, err := w.repo.PruneVersions(database.WithSuperadmin(ctx), keep)
	if err != nil {
		return fmt.Errorf("prune entity versions: %w", err)
	}
	if n > 0 {
		w.logger.Info("pruned entity versions", slog.Int64("removed", n), slog.Int("keep", keep))
	}
	return nil
}
