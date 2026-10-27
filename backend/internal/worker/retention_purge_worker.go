package worker

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
)

// RetentionPurgeWorker deletes expired server-side sessions once per day.
// session.Store.PurgeExpired already existed but had no scheduled caller.
type RetentionPurgeWorker struct {
	sessions *session.Store
	// extra purges run after the sessions (expired reset/invite tokens).
	extra  []func(context.Context) (int64, error)
	logger *slog.Logger
}

// AddPurge registers another purge to run on every tick; its error is logged,
// never fatal.
func (w *RetentionPurgeWorker) AddPurge(name string, fn func(context.Context) (int64, error)) {
	w.extra = append(w.extra, func(ctx context.Context) (int64, error) {
		n, err := fn(ctx)
		if err != nil {
			w.logger.Error("purge failed", slog.String("what", name), slog.Any("error", err))
		} else if n > 0 {
			w.logger.Info("purged expired rows", slog.String("what", name), slog.Int64("removed", n))
		}
		return n, err
	})
}

// NewRetentionPurgeWorker constructs the worker. sessions may be nil (purge skipped).
func NewRetentionPurgeWorker(sessions *session.Store, logger *slog.Logger) *RetentionPurgeWorker {
	return &RetentionPurgeWorker{sessions: sessions, logger: logger}
}

// Run executes one purge tick.
func (w *RetentionPurgeWorker) Run(ctx context.Context) error {
	adminCtx := database.WithSuperadmin(ctx)
	if w.sessions != nil {
		if n, err := w.sessions.PurgeExpired(adminCtx); err != nil {
			w.logger.Error("session purge failed", slog.Any("error", err))
		} else if n > 0 {
			w.logger.Info("purged expired sessions", slog.Int64("removed", n))
		}
	}
	for _, fn := range w.extra {
		_, _ = fn(adminCtx)
	}
	return nil
}
