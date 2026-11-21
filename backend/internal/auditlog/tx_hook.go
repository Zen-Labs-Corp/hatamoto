package auditlog

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
)

// TxHook returns a writer of ev inside tx for the repositories that audit
// their own writes. Only a registered sink's refusal is returned (it must fail
// the write); a failed default row is logged and the write goes on.
func TxHook(logger *slog.Logger) func(ctx context.Context, tx pgx.Tx, ev auditstate.Event) error {
	return func(ctx context.Context, tx pgx.Tx, ev auditstate.Event) error {
		err := Recorder{}.Record(ctx, tx, ev)
		if MustFail(err) {
			return err
		}
		if err != nil {
			logger.Error("admin audit write failed", "action", ev.Action, "error", err)
		}
		return nil
	}
}
