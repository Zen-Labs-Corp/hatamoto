package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/supervisor"
)

const backupUsageErr = "usage: hatamoto backup create [--config <path>] [--with-media] | list [--json] [--remote] | delete <id> | restore <id|path.tar> --admin-url <DSN> [--dry-run] [--config-out <path>]"

// runBackup runs `hatamoto backup ...`. list and delete need only the data
// directory, so they work with the database down; create opens it lazily.
func runBackup(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(backupUsageErr)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	switch args[0] {
	case "list":
		var asJSON, remote bool
		var of offsiteFlags
		pos, err := flagSet("backup list", args[1:], func(fs *flag.FlagSet) {
			fs.BoolVar(&asJSON, "json", false, "print JSON")
			fs.BoolVar(&remote, "remote", false, "list the backups in the off-site bucket (target from HATAMOTO_OFFSITE_*)")
			of.define(fs)
		})
		if err != nil || len(pos) != 0 {
			return errors.New("usage: hatamoto backup list [--json] [--remote] [--offsite-endpoint U] [--offsite-region R] [--offsite-bucket B] [--offsite-prefix P] [--offsite-access-key K]")
		}
		if remote {
			return listRemote(ctx, of, asJSON, out)
		}
		rows, err := newBackupEngine(nil, "", "", logger).List()
		if err != nil {
			return err
		}
		if asJSON {
			if rows == nil {
				rows = []backup.Info{}
			}
			return printJSON(out, rows)
		}
		if len(rows) == 0 {
			_, err = fmt.Fprintln(out, "no backups")
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tSTATE\tSIZE\tCREATED\tNOTE")
		for _, r := range rows {
			note := ""
			if r.MediaMissing > 0 {
				note = fmt.Sprintf("%d media files missing", r.MediaMissing)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Kind, r.State, humanBytes(r.Size), r.Created.UTC().Format(time.RFC3339), note)
		}
		return tw.Flush()
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: hatamoto backup delete <id>")
		}
		if !backup.ValidID(args[1]) {
			return fmt.Errorf("%q is not a backup id (see hatamoto backup list)", args[1])
		}
		if err := checkOwner(supervisor.NewLayout(config.ResolveDataDir())); err != nil {
			return err
		}
		if err := newBackupEngine(nil, "", "", logger).Delete(args[1]); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "deleted", args[1])
		return err
	case "restore":
		return runBackupRestore(ctx, args[1:], out, logger)
	case "create":
		ca, err := parseCreateArgs(args[1:])
		if err != nil {
			return err
		}
		if err := checkOwner(supervisor.NewLayout(config.ResolveDataDir())); err != nil {
			return err
		}
		db, err := openDB(ctx)
		if err != nil {
			return fmt.Errorf("backup create needs the database: %w", err)
		}
		defer db.Close()
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		ver, _ := buildInfo()
		eng := newBackupEngine(db.Pool(), cfg.Server.MediaUploadDir, ver, logger)
		if target, ok := mediaObjectTarget(); ok {
			fmt.Fprintf(out, "media: object storage (%s)\n", target)
		} else {
			fmt.Fprintf(out, "media %s\n", eng.MediaDirAbs())
		}
		info, err := eng.Create(ctx, backup.Request{Kind: backup.KindManual, ConfigPath: ca.configPath, WithMedia: ca.withMedia})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "backup %s\nsize %s\npath %s/backups/%s.tar\n", info.ID, humanBytes(info.Size), config.ResolveDataDir(), info.ID)
		for _, name := range []string{"secrets.env", "config.env"} {
			if slices.Contains(info.Parts, name) {
				fmt.Fprintf(out, "includes %s\n", name)
			}
		}
		fmt.Fprintln(out, "Note: the archive holds your database and secrets.env (when present). Keep it private.")
		return nil
	}
	return errors.New(backupUsageErr)
}

// mediaObjectTarget names the media bucket and prefix when MEDIA_STORAGE=s3
// and the settings are complete; never the secret.
func mediaObjectTarget() (string, bool) {
	if kind, _ := media.StorageFromEnv(os.Getenv); kind != "s3" {
		return "", false
	}
	t, err := media.S3TargetFromEnv(os.Getenv)
	if err != nil {
		return "", false
	}
	return t.Bucket + "/" + t.Prefix, true
}

type createArgs struct {
	configPath string
	withMedia  bool
}

// parseCreateFlags reads --config of `backup create`: the operator's env file
// to store verbatim (HATAMOTO_CONFIG_FILE when unset).
func parseCreateFlags(args []string) (configPath string, err error) {
	ca, err := parseCreateArgs(args)
	return ca.configPath, err
}

// parseCreateArgs reads the flags of `backup create`: --config and, for media
// in object storage, --with-media (copy the media bytes into the archive too).
func parseCreateArgs(args []string) (createArgs, error) {
	var ca createArgs
	pos, err := flagSet("backup create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ca.configPath, "config", "", "env file to store verbatim in the backup")
		fs.BoolVar(&ca.withMedia, "with-media", false, "also copy the media bytes when media is in object storage")
	})
	if err != nil || len(pos) != 0 {
		return createArgs{}, errors.New("usage: hatamoto backup create [--config <path>] [--with-media]")
	}
	return ca, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// listRemote prints the backups in the off-site bucket. It needs no database.
func listRemote(ctx context.Context, of offsiteFlags, asJSON bool, out io.Writer) error {
	t, err := of.target()
	if err != nil {
		return err
	}
	client, err := t.Client(nil)
	if err != nil {
		return err
	}
	rows, err := offsite.ListRemote(ctx, client, t.Prefix)
	if err != nil {
		return err
	}
	if asJSON {
		type row struct {
			ID           string    `json:"id"`
			Key          string    `json:"key"`
			Size         int64     `json:"size"`
			LastModified time.Time `json:"last_modified"`
		}
		list := make([]row, 0, len(rows))
		for _, r := range rows {
			list = append(list, row{r.ID, r.Key, r.Size, r.LastModified.UTC()})
		}
		return printJSON(out, list)
	}
	if len(rows) == 0 {
		_, err = fmt.Fprintln(out, "no backups in the bucket")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSIZE\tUPLOADED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, humanBytes(r.Size), r.LastModified.UTC().Format(time.RFC3339))
	}
	return tw.Flush()
}
