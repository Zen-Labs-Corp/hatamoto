package purgestate

import (
	"context"
	"strings"
	"testing"
)

type fake struct{}

func (*fake) Purge(context.Context, Request) error { return nil }

func reset(t *testing.T) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}

func TestRegister(t *testing.T) {
	reset(t)
	if _, _, ok := Registered(); ok {
		t.Fatal("registered before Register")
	}
	f := &fake{}
	Register("edge-1", f)
	name, p, ok := Registered()
	if !ok || name != "edge-1" || p != Purger(f) {
		t.Errorf("Registered = %q %v %v", name, p, ok)
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	mustPanic(t, "nil", func() { Register("ok", nil) })
	mustPanic(t, "typed nil", func() { Register("ok", (*fake)(nil)) })
	for _, n := range []string{"", "Bad", "a b", strings.Repeat("a", 33)} {
		mustPanic(t, "name "+n, func() { Register(n, &fake{}) })
	}
	Register("ok", &fake{})
	mustPanic(t, "twice", func() { Register("other", &fake{}) })
}

func TestRegisterAfterFreezePanics(t *testing.T) {
	reset(t)
	Freeze()
	mustPanic(t, "frozen", func() { Register("ok", &fake{}) })
}

func TestReset(t *testing.T) {
	reset(t)
	Register("ok", &fake{})
	Freeze()
	Reset()
	if _, _, ok := Registered(); ok {
		t.Error("Reset kept registration")
	}
	Register("ok", &fake{})
}
