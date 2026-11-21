package modhost

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CLIEnv is what a module command receives.
type CLIEnv struct {
	DB      *pgxpool.Pool
	Logger  *slog.Logger
	DataDir string
	Out     io.Writer
	Err     io.Writer
}

// CLICommand is a command a module adds to the hatamoto binary.
type CLICommand struct {
	Name  string
	Usage string
	Run   func(ctx context.Context, env *CLIEnv, args []string) error
}

// ExitError makes a command exit with a specific code.
type ExitError struct {
	Code int
	Err  error
}

func (e ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e ExitError) Unwrap() error { return e.Err }

var (
	commands    = map[string]CLICommand{}
	commandName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

func init() {
	onReset(func() { commands = map[string]CLICommand{} })
}

// RegisterCommand adds a command. It panics after Freeze, on an invalid or
// duplicate name and on a nil Run.
func RegisterCommand(c CLICommand) {
	register("cli: Register", func() {
		switch {
		case !commandName.MatchString(c.Name):
			panic(fmt.Sprintf("cli: invalid command name %q", c.Name))
		case isNil(c.Run):
			panic(fmt.Sprintf("cli: command %q has a nil Run", c.Name))
		}
		if _, dup := commands[c.Name]; dup {
			panic(fmt.Sprintf("cli: command %q registered twice", c.Name))
		}
		commands[c.Name] = c
	})
}

// Command returns the registered command with the given name.
func Command(name string) (CLICommand, bool) {
	mu.Lock()
	defer mu.Unlock()
	c, ok := commands[name]
	return c, ok
}

// CommandUsage returns one "  <name> <usage>" line per command, sorted, or "".
func CommandUsage() string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "  %s %s\n", n, commands[n].Usage)
	}
	return b.String()
}
