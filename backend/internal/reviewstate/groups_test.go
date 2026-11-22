package reviewstate

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	ok    bool
	err   error
	panic bool
}

func (f *fakeResolver) MayDecide(context.Context, string, string, string, string) (bool, error) {
	if f.panic {
		panic("boom")
	}
	return f.ok, f.err
}

func resetGroups(t *testing.T) {
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

func TestRegisterGroupsPanics(t *testing.T) {
	resetGroups(t)
	RegisterGroups(&fakeResolver{})
	mustPanic(t, "twice", func() { RegisterGroups(&fakeResolver{}) })

	resetGroups(t)
	mustPanic(t, "nil", func() { RegisterGroups(nil) })
	mustPanic(t, "typed nil", func() { RegisterGroups((*fakeResolver)(nil)) })

	resetGroups(t)
	Freeze()
	mustPanic(t, "after Freeze", func() { RegisterGroups(&fakeResolver{}) })
}

func TestResetClearsGroups(t *testing.T) {
	resetGroups(t)
	RegisterGroups(&fakeResolver{ok: true})
	Freeze()
	Reset()
	if GroupsRegistered() {
		t.Fatal("Reset must clear the resolver")
	}
	RegisterGroups(&fakeResolver{}) // frozen was cleared too
}

func TestMayDecide(t *testing.T) {
	ctx := context.Background()
	resetGroups(t)
	if ok, err := MayDecide(ctx, "u", "editor", "legal", "e"); ok || err != nil {
		t.Fatalf("no resolver: got %v, %v; want false, nil", ok, err)
	}
	for _, ok := range []bool{true, false} {
		resetGroups(t)
		RegisterGroups(&fakeResolver{ok: ok})
		if got, err := MayDecide(ctx, "u", "editor", "legal", "e"); got != ok || err != nil {
			t.Fatalf("got %v, %v; want %v, nil", got, err, ok)
		}
	}
	resetGroups(t)
	RegisterGroups(&fakeResolver{ok: true, err: errors.New("down")})
	if ok, err := MayDecide(ctx, "u", "editor", "legal", "e"); ok || !errors.Is(err, ErrGroupResolver) {
		t.Fatalf("error: got %v, %v; want false, ErrGroupResolver", ok, err)
	}
	resetGroups(t)
	RegisterGroups(&fakeResolver{ok: true, panic: true})
	if ok, err := MayDecide(ctx, "u", "editor", "legal", "e"); ok || !errors.Is(err, ErrGroupResolver) {
		t.Fatalf("panic: got %v, %v; want false, ErrGroupResolver", ok, err)
	}
}
