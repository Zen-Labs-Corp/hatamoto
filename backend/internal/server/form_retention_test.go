package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/worker"
)

// purgeFormRepo records that the retention purge reached the form repository.
type purgeFormRepo struct {
	form.Repository
	purged int
}

func (r *purgeFormRepo) PurgeExpiredSubmissions(context.Context) (int64, error) {
	r.purged++
	return 3, nil
}

func TestFormRetentionRegisteredOnDailyPurge(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &purgeFormRepo{}
	s := &Server{logger: logger, formService: form.NewService(repo, logger)}

	w := worker.NewRetentionPurgeWorker(nil, logger)
	s.addFormRetention(w)
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.purged != 1 {
		t.Fatalf("form purge ran %d times, want 1", repo.purged)
	}
}
