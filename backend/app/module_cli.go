package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/modhost"
)

// coreWords are the first words handled by the switch in parseArgs. Keep in
// sync with it: a module command never shadows one of them.
var coreWords = []string{
	"builder", "doctor", "locales", "serve", "supervise", "update", "migrate", "user",
	"admin-url", "media", "static", "routes", "plugins", "version", "healthcheck",
	"help", "-h", "--help", "sanitize-html-sections",
}

// coreCommandWords lists every first word parseArgs knows: the switch cases,
// packageCommands and opsCommands.
func coreCommandWords() map[string]bool {
	words := make(map[string]bool, len(coreWords)+len(packageCommands)+len(opsCommands))
	for _, w := range coreWords {
		words[w] = true
	}
	for w := range packageCommands {
		words[w] = true
	}
	for w := range opsCommands {
		words[w] = true
	}
	return words
}

// moduleCommand reports whether args[0] is a registered module command that
// no core command shadows.
func moduleCommand(args []string) bool {
	if len(args) == 0 || coreCommandWords()[args[0]] {
		return false
	}
	_, ok := modhost.Command(args[0])
	return ok
}

// openModuleDB opens the database for a module command; tests replace it.
var openModuleDB = func(ctx context.Context) (*pgxpool.Pool, func(), error) {
	db, err := openDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	return db.Pool(), db.Close, nil
}

// runModule runs the registered module command args[0] with args[1:]. A
// modhost.ExitError becomes an exitError with its code (after printing its
// message); a panic becomes an error.
func runModule(ctx context.Context, args []string, _ *slog.Logger) (err error) {
	cmd, ok := modhost.Command(args[0])
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	pool, closeDB, err := openModuleDB(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	defer publishModulePool(pool)() // db.Pool() is env.DB until the command returns
	env := &modhost.CLIEnv{
		DB:      pool,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		DataDir: config.ResolveDataDir(),
		Out:     os.Stdout,
		Err:     os.Stderr,
	}
	return callModuleCommand(ctx, cmd, env, args[1:])
}

func callModuleCommand(ctx context.Context, cmd modhost.CLICommand, env *modhost.CLIEnv, args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("command %q panicked: %v", cmd.Name, r)
		}
	}()
	err = cmd.Run(ctx, env, args)
	var ee modhost.ExitError
	if errors.As(err, &ee) {
		if ee.Err != nil {
			fmt.Fprintln(env.Err, ee.Err)
		}
		code := ee.Code
		if code < 1 || code > 255 {
			code = 1 // os.Exit wraps modulo 256: 256 would read as success
		}
		return exitError{code}
	}
	return err
}
