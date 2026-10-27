package worker

import (
	"context"
	"fmt"
	"log/slog"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// EntityVersionWorker coalesces identical-consecutive entity versions and keeps
// only the newest N per entity, so hatamoto_entity_versions stays bounded.
type EntityVersionWorker struct {
	repo   entity.EntityRepository
	keep   int
	logger *slog.Logger
}

func NewEntityVersionWorker(repo entity.EntityRepository, keep int, logger *slog.Logger) *EntityVersionWorker {
	if keep <= 0 {
		keep = 20
	}
	return &EntityVersionWorker{repo: repo, keep: keep, logger: logger}
}

func (w *EntityVersionWorker) Run(ctx context.Context) error {
	if w.repo == nil {
		return nil
	}
	n, err := w.repo.PruneVersions(database.WithSuperadmin(ctx), w.keep)
	if err != nil {
		return fmt.Errorf("prune entity versions: %w", err)
	}
	if n > 0 {
		w.logger.Info("pruned entity versions", slog.Int64("removed", n), slog.Int("keep", w.keep))
	}
	return nil
}
