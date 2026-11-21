package modhost

import (
	"context"
	"errors"
	"testing"
)

func okRun(context.Context, *CLIEnv, []string) error { return nil }

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}

func TestCommandRegistry(t *testing.T) {
	t.Cleanup(Reset)
	RegisterCommand(CLICommand{Name: "zeta", Usage: "z", Run: okRun})
	RegisterCommand(CLICommand{Name: "alpha", Usage: "a <x>", Run: okRun})
	if _, ok := Command("alpha"); !ok {
		t.Fatal("alpha not found")
	}
	if _, ok := Command("nope"); ok {
		t.Fatal("unexpected command")
	}
	if got, want := CommandUsage(), "  alpha a <x>\n  zeta z\n"; got != want {
		t.Fatalf("usage %q want %q", got, want)
	}
	mustPanic(t, "dup", func() { RegisterCommand(CLICommand{Name: "alpha", Run: okRun}) })
	mustPanic(t, "bad name", func() { RegisterCommand(CLICommand{Name: "Bad", Run: okRun}) })
	mustPanic(t, "nil run", func() { RegisterCommand(CLICommand{Name: "nilrun"}) })
	Freeze()
	mustPanic(t, "frozen", func() { RegisterCommand(CLICommand{Name: "late", Run: okRun}) })
	Reset()
	if CommandUsage() != "" {
		t.Fatal("reset did not clear")
	}
}

func TestExitError(t *testing.T) {
	inner := errors.New("boom")
	e := ExitError{Code: 3, Err: inner}
	if !errors.Is(e, inner) || e.Error() != "boom" {
		t.Fatalf("unwrap/error: %v", e)
	}
	if (ExitError{Code: 2}).Error() != "exit status 2" {
		t.Fatal("bare message")
	}
}
