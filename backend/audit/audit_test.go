package audit_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
)

type fakeSink struct{ got []audit.Event }

func (f *fakeSink) Record(_ context.Context, _ pgx.Tx, ev audit.Event) error {
	f.got = append(f.got, ev)
	return nil
}

var _ audit.Sink = (*fakeSink)(nil)

func reset(t *testing.T) {
	t.Helper()
	audit.ResetForTest()
	t.Cleanup(audit.ResetForTest)
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: no panic", name)
		}
	}()
	fn()
}

func TestRegister(t *testing.T) {
	reset(t)
	audit.Register(&fakeSink{})
	mustPanic(t, "twice", func() { audit.Register(&fakeSink{}) })
}

func TestRegisterRejectsNil(t *testing.T) {
	reset(t)
	mustPanic(t, "nil", func() { audit.Register(nil) })
	mustPanic(t, "typed nil", func() { audit.Register((*fakeSink)(nil)) })
}

func TestRegisterAfterFreeze(t *testing.T) {
	reset(t)
	audit.FreezeForTest()
	mustPanic(t, "frozen", func() { audit.Register(&fakeSink{}) })
}
