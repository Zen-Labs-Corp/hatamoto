package app

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/modhost"
)

func registerNoop(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(modhost.Reset)
	for _, n := range names {
		modhost.RegisterCommand(modhost.CLICommand{Name: n, Usage: "does " + n,
			Run: func(context.Context, *modhost.CLIEnv, []string) error { return nil }})
	}
}

func TestParseArgsModuleCommand(t *testing.T) {
	registerNoop(t, "hello", "migrate", "user")
	if cmd, err := parseArgs([]string{"hello", "--x"}); err != nil || cmd != cmdModule {
		t.Fatalf("module command: %v %v", cmd, err)
	}
	// Core words win, with their own argument rules.
	if cmd, err := parseArgs([]string{"migrate"}); err != nil || cmd != cmdMigrate {
		t.Fatalf("migrate: %v %v", cmd, err)
	}
	if cmd, _ := parseArgs([]string{"user", "bogus"}); cmd == cmdModule {
		t.Fatal("core word user was routed to a module")
	}
	if _, err := parseArgs([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestUsageTextUnchangedWithoutModules(t *testing.T) {
	t.Cleanup(modhost.Reset)
	if usageText() != usage {
		t.Fatal("usage changed with nothing registered")
	}
	if _, err := parseArgs(nil); err == nil || err.Error() != usage {
		t.Fatalf("empty args error changed: %v", err)
	}
	registerNoop(t, "hello")
	got := usageText()
	if !strings.HasPrefix(got, usage) || !strings.HasSuffix(got, "  hello does hello") {
		t.Fatalf("usage with a module command:\n%s", got[len(usage):])
	}
	if _, err := parseArgs([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "hello does hello") {
		t.Fatalf("unknown command usage lacks module commands: %v", err)
	}
}
