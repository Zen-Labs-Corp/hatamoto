package authzstate

import "testing"

type thing struct{}

func fresh(t *testing.T) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	fn()
}

func TestLoadDefaultNil(t *testing.T) {
	fresh(t)
	if Load() != nil {
		t.Error("Load must be nil by default")
	}
}

func TestSetThenLoad(t *testing.T) {
	fresh(t)
	v := &thing{}
	Set(v)
	if Load() != any(v) {
		t.Error("Load did not return the registered value")
	}
}

func TestSetPanics(t *testing.T) {
	fresh(t)
	mustPanic(t, func() { Set(nil) })
	mustPanic(t, func() { Set((*thing)(nil)) })
	mustPanic(t, func() { Set((func())(nil)) })
	mustPanic(t, func() { Set(map[string]int(nil)) })
	if Load() != nil {
		t.Error("a rejected Set must not install anything")
	}
	Set(&thing{})
	mustPanic(t, func() { Set(&thing{}) })
}

func TestSetAfterFreezePanics(t *testing.T) {
	fresh(t)
	Freeze()
	mustPanic(t, func() { Set(&thing{}) })
}

func TestResetClears(t *testing.T) {
	fresh(t)
	Set(&thing{})
	Freeze()
	Reset()
	if Load() != nil {
		t.Error("Reset must clear the slot")
	}
	Set(&thing{})
}
