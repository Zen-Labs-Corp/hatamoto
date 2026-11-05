package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

type keepRecorder struct {
	entity.EntityRepository
	keep  int
	calls int
}

func (r *keepRecorder) PruneVersions(_ context.Context, keep int) (int64, error) {
	r.keep = keep
	r.calls++
	return 0, nil
}

func TestEntityVersionWorkerKeep(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	val := func(n int) func(context.Context) (int, error) {
		return func(context.Context) (int, error) { return n, nil }
	}
	for name, tc := range map[string]struct {
		keep func(context.Context) (int, error)
		want int
	}{
		"setting":   {val(80), 80},
		"too small": {val(3), entity.VersionKeepMin},
		"zero":      {val(0), entity.VersionKeepMin},
		"too large": {val(9000), entity.VersionKeepMax},
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

func TestEntityVersionWorkerSkipsOnReadError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &keepRecorder{}
	keep := func(context.Context) (int, error) { return 0, errors.New("boom") }
	if err := NewEntityVersionWorker(repo, keep, logger).Run(t.Context()); err == nil {
		t.Fatal("Run returned nil, want the read error")
	}
	if repo.calls != 0 {
		t.Fatalf("PruneVersions called %d times, want 0", repo.calls)
	}
}
