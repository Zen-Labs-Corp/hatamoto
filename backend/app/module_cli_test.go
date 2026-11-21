package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
)

func TestModuleCommandShadowing(t *testing.T) {
	t.Cleanup(modhost.Reset)
	run := func(context.Context, *modhost.CLIEnv, []string) error { return nil }
	modhost.RegisterCommand(modhost.CLICommand{Name: "migrate", Run: run})
	modhost.RegisterCommand(modhost.CLICommand{Name: "hello", Run: run})
	if moduleCommand([]string{"migrate"}) {
		t.Error("core command migrate was shadowed")
	}
	if !moduleCommand([]string{"hello", "x"}) {
		t.Error("module command hello not found")
	}
	if moduleCommand([]string{"nope"}) || moduleCommand(nil) {
		t.Error("unregistered command matched")
	}
}

func TestCoreCommandWords(t *testing.T) {
	w := coreCommandWords()
	for _, want := range []string{"serve", "migrate", "version", "help"} {
		if !w[want] {
			t.Errorf("missing %q", want)
		}
	}
	for k := range opsCommands {
		if !w[k] {
			t.Errorf("missing ops %q", k)
		}
	}
	for k := range packageCommands {
		if !w[k] {
			t.Errorf("missing package %q", k)
		}
	}
}

func TestCallModuleCommand(t *testing.T) {
	ctx := context.Background()
	var out, errw bytes.Buffer
	env := &modhost.CLIEnv{Out: &out, Err: &errw}
	mk := func(f func(context.Context, *modhost.CLIEnv, []string) error) modhost.CLICommand {
		return modhost.CLICommand{Name: "x", Run: f}
	}

	err := callModuleCommand(ctx, mk(func(_ context.Context, e *modhost.CLIEnv, a []string) error {
		_, _ = e.Out.Write([]byte(strings.Join(a, ",")))
		return modhost.ExitError{Code: 3, Err: errors.New("refused")}
	}), env, []string{"a", "b"})
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 3 {
		t.Fatalf("err = %v, want exitError{3}", err)
	}
	if out.String() != "a,b" || !strings.Contains(errw.String(), "refused") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}

	err = callModuleCommand(ctx, mk(func(context.Context, *modhost.CLIEnv, []string) error { panic("boom") }), env, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic not mapped: %v", err)
	}

	plain := errors.New("plain")
	if err := callModuleCommand(ctx, mk(func(context.Context, *modhost.CLIEnv, []string) error { return plain }), env, nil); err != plain {
		t.Fatalf("plain error changed: %v", err)
	}
}

func TestRunModuleOpenError(t *testing.T) {
	t.Cleanup(modhost.Reset)
	modhost.RegisterCommand(modhost.CLICommand{Name: "hello", Run: func(context.Context, *modhost.CLIEnv, []string) error { return nil }})
	old := openModuleDB
	t.Cleanup(func() { openModuleDB = old })
	openModuleDB = func(context.Context) (*pgxpool.Pool, func(), error) { return nil, nil, errors.New("no db") }
	if err := runModule(context.Background(), []string{"hello"}, nil); err == nil || err.Error() != "no db" {
		t.Fatalf("err = %v", err)
	}
}

func TestCallModuleCommandClampsExitCode(t *testing.T) {
	env := &modhost.CLIEnv{Out: io.Discard, Err: io.Discard}
	for code, want := range map[int]int{-1: 1, 0: 1, 1: 1, 255: 255, 256: 1, 1000: 1} {
		cmd := modhost.CLICommand{Name: "x", Run: func(context.Context, *modhost.CLIEnv, []string) error {
			return modhost.ExitError{Code: code}
		}}
		var ee exitError
		if err := callModuleCommand(context.Background(), cmd, env, nil); !errors.As(err, &ee) || ee.code != want {
			t.Errorf("code %d: err = %v, want exit %d", code, err, want)
		}
	}
}
