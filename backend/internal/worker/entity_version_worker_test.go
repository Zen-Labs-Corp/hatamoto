package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

type keepRecorder struct {
	entity.EntityRepository
	keep int
}

func (r *keepRecorder) PruneVersions(_ context.Context, keep int) (int64, error) {
	r.keep = keep
	return 0, nil
}

func TestEntityVersionWorkerKeep(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for name, tc := range map[string]struct {
		keep func(context.Context) int
		want int
	}{
		"setting":   {func(context.Context) int { return 80 }, 80},
		"zero":      {func(context.Context) int { return 0 }, entity.VersionKeepDefault},
		"too small": {func(context.Context) int { return 3 }, entity.VersionKeepDefault},
		"too large": {func(context.Context) int { return 9000 }, entity.VersionKeepDefault},
		"nil":       {nil, entity.VersionKeepDefault},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &keepRecorder{}
			if err := NewEntityVersionWorker(repo, tc.keep, logger).Run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if repo.keep != tc.want {
				t.Fatalf("prune keep = %d, want %d", repo.keep, tc.want)
			}
		})
	}
}
