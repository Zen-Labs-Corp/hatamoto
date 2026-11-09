package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
)

const mediaUsageErr = "usage: hatamoto media migrate --to s3|local [--dry-run]"

const mediaUsage = `  media migrate --to s3|local [--dry-run]
                                   copy the media library between local disk and object storage (verified by sha256, resumable, the source is kept)`

type mediaMigrateFlags struct {
	to     string
	dryRun bool
}

func parseMediaMigrateArgs(args []string) (mediaMigrateFlags, error) {
	var f mediaMigrateFlags
	pos, err := flagSet("media migrate", args, func(fs *flag.FlagSet) {
		fs.StringVar(&f.to, "to", "", "destination: s3 or local")
		fs.BoolVar(&f.dryRun, "dry-run", false, "report what would be copied, write nothing")
	})
	if err != nil || len(pos) != 0 || (f.to != "s3" && f.to != "local") {
		return f, errors.New(mediaUsageErr)
	}
	return f, nil
}

// mediaMigratePlan is everything one `media migrate` run needs; runMedia
// builds it from the environment and tests build it from fakes.
type mediaMigratePlan struct {
	mediaMigrateFlags
	from, dest     media.BlobStore
	bucket, source string // printed: the resolved bucket/prefix and the kept source
	list           func(ctx context.Context) ([]*media.Media, error)
}

// runMedia runs `hatamoto media migrate`.
func runMedia(ctx context.Context, databaseURL string, args []string, out io.Writer) error {
	if len(args) < 2 || args[0] != "media" || args[1] != "migrate" {
		return errors.New(mediaUsageErr)
	}
	f, err := parseMediaMigrateArgs(args[2:])
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Database.URL = databaseURL

	// The S3 side comes from the HATAMOTO_MEDIA_S3_* variables whatever
	// MEDIA_STORAGE says: migrating happens before the switch.
	target, err := media.S3TargetFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	s3store, err := media.NewS3Store(target, "/media", nil)
	if err != nil {
		return err
	}
	dir := cfg.Server.MediaUploadDir
	local, err := media.NewLocalStore(dir, "/media")
	if err != nil {
		return err
	}
	p := mediaMigratePlan{mediaMigrateFlags: f, from: local, dest: s3store, bucket: target.Bucket + "/" + target.Prefix, source: dir}
	if f.to == "local" {
		p.from, p.dest, p.source = s3store, local, p.bucket
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	repo := media.NewPostgresRepository(db)
	p.list = func(ctx context.Context) ([]*media.Media, error) { return listAllMedia(ctx, repo) }
	return mediaMigrate(ctx, p, out)
}

// listAllMedia pages through the whole library.
func listAllMedia(ctx context.Context, repo media.Repository) ([]*media.Media, error) {
	const page = 500
	var all []*media.Media
	for {
		res, err := repo.List(ctx, &media.ListMediaFilter{}, page, len(all))
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) < page {
			return all, nil
		}
	}
}

func mediaMigrate(ctx context.Context, p mediaMigratePlan, out io.Writer) error {
	rows, err := p.list(ctx)
	if err != nil {
		return fmt.Errorf("list media: %w", err)
	}
	keys := media.MediaKeys(rows)
	from, to := "local", "s3"
	if p.to == "local" {
		from, to = "s3", "local"
	}
	dry := ""
	if p.dryRun {
		dry = " (dry run)"
	}
	fmt.Fprintf(out, "media migrate: %s -> %s%s\n", from, to, dry)
	fmt.Fprintf(out, "  bucket  %s\n", p.bucket)

	total := len(keys)
	step := max(total/10, 1)
	width := len(strconv.Itoa(total))
	rep, err := media.Migrate(ctx, media.MigrateOptions{From: p.from, To: p.dest, Keys: keys, DryRun: p.dryRun,
		Progress: func(done, total int, bytes int64) {
			if done%step == 0 || done == total {
				suffix := ""
				if done == total {
					suffix = "  done"
				}
				fmt.Fprintf(out, "  [%*d/%d] %s%s\n", width, done, total, humanBytes(bytes), suffix)
			}
		}})
	if err != nil {
		fmt.Fprintf(out, "\ninterrupted: %v\nrun the command again to resume\n", err)
		return err
	}

	copied := "copied"
	if p.dryRun {
		copied = "would copy"
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%-10s %7s   verified sha256\n", copied, thousands(rep.Copied))
	fmt.Fprintf(out, "%-10s %7s   already at the destination, same sha256\n", "skipped", thousands(rep.Skipped))
	fmt.Fprintf(out, "%-10s %7s\n", "failed", thousands(rep.Failed))
	for _, e := range rep.Errors {
		fmt.Fprintf(out, "  error: %v\n", e)
	}
	fmt.Fprintf(out, "source kept: %s\n", p.source)
	if rep.Failed > 0 {
		fmt.Fprintln(out, "some files failed: fix the cause and run the command again")
		return exitError{1}
	}
	if !p.dryRun {
		if p.to == "s3" {
			fmt.Fprintln(out, "next: set MEDIA_STORAGE=s3 and restart")
		} else {
			fmt.Fprintln(out, "next: unset MEDIA_STORAGE (or set it to local) and restart")
		}
	}
	return nil
}
