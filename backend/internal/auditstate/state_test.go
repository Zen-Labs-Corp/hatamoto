package auditstate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
)

type fake struct{}

func (*fake) Record(context.Context, pgx.Tx, auditstate.Event) error { return nil }

func reset(t *testing.T) {
	t.Helper()
	auditstate.Reset()
	t.Cleanup(auditstate.Reset)
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s: no panic", name)
		}
		if s, ok := r.(string); !ok || !strings.HasPrefix(s, "audit: ") {
			t.Fatalf("%s: panic %v lacks the audit: prefix", name, r)
		}
	}()
	fn()
}

func TestRegisterAndReset(t *testing.T) {
	reset(t)
	if auditstate.Registered() != nil {
		t.Fatal("sink registered at start")
	}
	f := &fake{}
	auditstate.Register(f)
	if auditstate.Registered() != auditstate.Sink(f) {
		t.Fatal("Registered did not return the sink")
	}
	auditstate.Reset()
	if auditstate.Registered() != nil {
		t.Fatal("Registered after Reset is not nil")
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	mustPanic(t, "nil", func() { auditstate.Register(nil) })
	mustPanic(t, "typed nil", func() { auditstate.Register((*fake)(nil)) })
	auditstate.Register(&fake{})
	mustPanic(t, "twice", func() { auditstate.Register(&fake{}) })
	auditstate.Reset()
	auditstate.Freeze()
	mustPanic(t, "frozen", func() { auditstate.Register(&fake{}) })
}
