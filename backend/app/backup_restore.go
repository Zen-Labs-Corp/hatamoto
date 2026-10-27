package app

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/supervisor"
)

const backupRestoreUsage = "usage: hatamoto backup restore <id|path.tar> --admin-url <DSN> [--dry-run] [--config-out <path>]"

// restoreArgs are the parsed flags and source of `backup restore`.
type restoreArgs struct {
	source, adminURL, configOut string
	dryRun                      bool
}

func parseRestoreArgs(args []string) (restoreArgs, error) {
	var a restoreArgs
	pos, err := flagSet("backup restore", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&a.dryRun, "dry-run", false, "run every check, write nothing")
		fs.StringVar(&a.adminURL, "admin-url", "", "superuser DSN for the role bootstrap")
		fs.StringVar(&a.configOut, "config-out", "", "absolute path to write the saved config to; without it the config is not written")
	})
	if err != nil || len(pos) != 1 || a.adminURL == "" {
		return a, errors.New(backupRestoreUsage)
	}
	a.source = pos[0]
	return a, nil
}

// runBackupRestore restores a backup onto a clean machine. It does not open
// the database through openDB (that is for a migrated one): the engine checks
// the empty target itself and opens its own pool after the core restore.
func runBackupRestore(ctx context.Context, args []string, out io.Writer, logger *slog.Logger) error {
	a, err := parseRestoreArgs(args)
	if err != nil {
		return err
	}
	if err := checkOwner(supervisor.NewLayout(config.ResolveDataDir())); err != nil {
		return err
	}
	ver, _ := buildInfo()
	plan, err := backup.Restore(ctx, backup.RestoreOptions{
		Data:        config.ResolveDataDir(),
		MediaDir:    mediaDirOr(os.Getenv("MEDIA_UPLOAD_DIR")),
		SecretsDir:  secretsDirFromEnv(),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Version:     ver,
		Run:         backup.OSExec,
		Trust:       trustIDs(logger),
		Log:         logger,
	}, backup.RestoreRequest{Source: a.source, AdminURL: a.adminURL, ConfigOut: a.configOut, DryRun: a.dryRun})
	if err != nil {
		return err
	}
	plan.Print(out)
	return nil
}
