package audit_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/audit"
)

func TestRecordNilTx(t *testing.T) {
	reset(t)
	f := &fakeSink{}
	audit.Register(f)
	err := audit.Record(context.Background(), nil, audit.Event{Action: "x"})
	if !errors.Is(err, audit.ErrNoTx) {
		t.Fatalf("err = %v, want ErrNoTx", err)
	}
	if len(f.got) != 0 {
		t.Fatalf("sink called %d times, want 0", len(f.got))
	}
}
