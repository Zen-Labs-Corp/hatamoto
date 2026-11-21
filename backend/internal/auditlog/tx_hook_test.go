package auditlog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
)

type refuseSink struct{}

func (refuseSink) Record(context.Context, pgx.Tx, auditstate.Event) error {
	return errors.New("refused")
}

// brokenTx fails to open the savepoint the default row is written under.
type brokenTx struct{ pgx.Tx }

func (brokenTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("tx aborted") }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTxHookReturnsASinkRefusal(t *testing.T) {
	auditstate.Register(refuseSink{})
	t.Cleanup(auditstate.Reset)

	err := TxHook(quiet())(context.Background(), nil, auditstate.Event{Action: "entity.create"})
	if !errors.Is(err, ErrSink) {
		t.Fatalf("err = %v, want ErrSink", err)
	}
}

func TestTxHookKeepsGoingWhenTheDefaultRowFails(t *testing.T) {
	if err := TxHook(quiet())(context.Background(), brokenTx{}, auditstate.Event{Action: "entity.create"}); err != nil {
		t.Fatalf("a failed default row must not fail the write: %v", err)
	}
}

func TestTxHookWithoutSinkOrPoolIsQuiet(t *testing.T) {
	if err := TxHook(quiet())(context.Background(), nil, auditstate.Event{Action: "entity.create"}); err != nil {
		t.Fatal(err)
	}
}
