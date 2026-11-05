package app

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/supervisor"
)

const backupRestoreUsage = "usage: hatamoto backup restore (<id|path.tar> | --from-offsite <id>) --admin-url <DSN> [--dry-run] [--config-out <path>]" +
	" [--offsite-endpoint U] [--offsite-region R] [--offsite-bucket B] [--offsite-prefix P] [--offsite-access-key K]"

// offsiteFlags are the flags that name an off-site target on the command line.
// Each overrides its HATAMOTO_OFFSITE_* variable. The secret key has no flag so
// it never shows in a process list or shell history: it comes from the
// environment only.
type offsiteFlags struct{ endpoint, region, bucket, prefix, accessKey string }

func (o *offsiteFlags) define(fs *flag.FlagSet) {
	fs.StringVar(&o.endpoint, "offsite-endpoint", "", "off-site endpoint URL (default "+offsite.EnvEndpoint+")")
	fs.StringVar(&o.region, "offsite-region", "", "off-site region (default "+offsite.EnvRegion+")")
	fs.StringVar(&o.bucket, "offsite-bucket", "", "off-site bucket (default "+offsite.EnvBucket+")")
	fs.StringVar(&o.prefix, "offsite-prefix", "", "off-site key prefix (default "+offsite.EnvPrefix+")")
	fs.StringVar(&o.accessKey, "offsite-access-key", "", "off-site access key (default "+offsite.EnvAccessKey+")")
}

// target reads the target from the flags, then the environment. It never reads
// the database: a restore runs on a machine that has none.
func (o offsiteFlags) target() (offsite.Target, error) {
	over := map[string]string{
		offsite.EnvEndpoint: o.endpoint, offsite.EnvRegion: o.region, offsite.EnvBucket: o.bucket,
		offsite.EnvPrefix: o.prefix, offsite.EnvAccessKey: o.accessKey,
	}
	return offsite.TargetFromEnv(func(k string) string {
		if v := over[k]; v != "" {
			return v
		}
		return os.Getenv(k)
	})
}

// restoreArgs are the parsed flags and source of `backup restore`.
type restoreArgs struct {
	source, adminURL, configOut string
	fromOffsite                 string
	dryRun                      bool
	offsite                     offsiteFlags
}

func parseRestoreArgs(args []string) (restoreArgs, error) {
	var a restoreArgs
	pos, err := flagSet("backup restore", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&a.dryRun, "dry-run", false, "run every check, write nothing")
		fs.StringVar(&a.adminURL, "admin-url", "", "superuser DSN for the role bootstrap")
		fs.StringVar(&a.configOut, "config-out", "", "absolute path to write the saved config to; without it the config is not written")
		fs.StringVar(&a.fromOffsite, "from-offsite", "", "restore backup `id` from the off-site bucket instead of a local archive")
		a.offsite.define(fs)
	})
	want := 1 // exactly one of the positional source and --from-offsite
	if a.fromOffsite != "" {
		want = 0
	}
	if err != nil || len(pos) != want || a.adminURL == "" {
		return a, errors.New(backupRestoreUsage)
	}
	if want == 1 {
		a.source = pos[0]
	}
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
	if a.fromOffsite != "" {
		path, cleanup, err := fetchOffsite(ctx, a)
		if err != nil {
			return err
		}
		defer cleanup()
		a.source = path
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

// fetchOffsite downloads the backup named by --from-offsite into the backups
// directory, with its checksum checked against the one recorded in the bucket.
// cleanup removes the file, and the directory when this call created it, so a
// dry run leaves nothing behind.
func fetchOffsite(ctx context.Context, a restoreArgs) (path string, cleanup func(), err error) {
	t, err := a.offsite.target()
	if err != nil {
		return "", nil, err
	}
	client, err := t.Client(nil)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(config.ResolveDataDir(), "backups")
	_, statErr := os.Stat(dir)
	path, err = offsite.Download(ctx, client, t.Prefix, a.fromOffsite, dir)
	if err != nil {
		if statErr != nil {
			_ = os.Remove(dir) // empty: only created for this download
		}
		return "", nil, err
	}
	return path, func() {
		_ = os.Remove(path)
		if statErr != nil {
			_ = os.Remove(dir) // fails, harmlessly, when something else is in it
		}
	}, nil
}
