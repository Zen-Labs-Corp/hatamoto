package audit

import (
	"context"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
)

// Record writes ev inside tx: the default audit table row through tx (a
// failed row does not abort tx) and then the registered sink, synchronously,
// with the same tx. A sink error is returned wrapped in ErrSink; return it
// from your transaction so the change rolls back. A failed table row is
// returned wrapped in ErrTable. Record needs a transaction: with a nil tx it
// returns ErrNoTx and writes nothing.
func Record(ctx context.Context, tx pgx.Tx, ev Event) error {
	if tx == nil {
		return auditstate.ErrNoTx
	}
	return auditlog.Recorder{}.Record(ctx, tx, ev)
}
