package cli_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/module/cli"
)

func run(context.Context, *cli.Env, []string) error { return nil }

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return
}

func TestRegisterAndLookup(t *testing.T) {
	t.Cleanup(cli.ResetForTest)
	cli.Register(cli.Command{Name: "b-cmd", Usage: "second", Run: run})
	cli.Register(cli.Command{Name: "a-cmd", Usage: "first", Run: run})
	if _, ok := cli.Lookup("a-cmd"); !ok {
		t.Fatal("a-cmd missing")
	}
	if got, want := cli.Usage(), "  a-cmd first\n  b-cmd second\n"; got != want {
		t.Fatalf("usage %q want %q", got, want)
	}
}

func TestRegisterRejects(t *testing.T) {
	t.Cleanup(cli.ResetForTest)
	cli.Register(cli.Command{Name: "dup", Run: run})
	if !panics(func() { cli.Register(cli.Command{Name: "dup", Run: run}) }) {
		t.Error("duplicate did not panic")
	}
	if !panics(func() { cli.Register(cli.Command{Name: "Bad Name", Run: run}) }) {
		t.Error("invalid name did not panic")
	}
	if !panics(func() { cli.Register(cli.Command{Name: "nilrun"}) }) {
		t.Error("nil Run did not panic")
	}
	cli.FreezeForTest()
	if !panics(func() { cli.Register(cli.Command{Name: "late", Run: run}) }) {
		t.Error("register after freeze did not panic")
	}
}

func TestExit(t *testing.T) {
	inner := errors.New("bad")
	var ee modhost.ExitError
	if !errors.As(cli.Exit(4, inner), &ee) || ee.Code != 4 || !errors.Is(ee, inner) {
		t.Fatalf("Exit: %+v", ee)
	}
}
