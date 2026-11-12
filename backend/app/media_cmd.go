package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
)

const mediaUsageErr = "usage: hatamoto media migrate --to s3|local [--dry-run]\n       hatamoto media sweep [--older-than 24h] [--apply]\n       hatamoto media adopt [<dir>] [--older-than 24h] [--apply]"

const mediaUsage = `  media migrate --to s3|local [--dry-run]
                                   copy the media library between local disk and object storage (verified by sha256, resumable, the source is kept)
  media sweep [--older-than 24h] [--apply]
                                   list (and with --apply delete) files in object storage that no media record refers to (dry run by default, at least 24h old; do not run --apply during an import or a publish)
  media adopt [<dir>] [--older-than 24h] [--apply]
                                   list (and with --apply create) media records for files in the media store that have none; <dir> is a path inside the store (default: all; dry run by default; younger files are left alone; do not run --apply during an import or a publish)`

// maxSweepListed is how many orphan keys a dry run prints.
const maxSweepListed = 50

type mediaSweepFlags struct {
	olderThan time.Duration
	apply     bool
}

func parseMediaSweepArgs(args []string) (mediaSweepFlags, error) {
	f := mediaSweepFlags{olderThan: 24 * time.Hour}
	pos, err := flagSet("media sweep", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&f.olderThan, "older-than", f.olderThan, "only files at least this old (minimum 24h)")
		fs.BoolVar(&f.apply, "apply", false, "delete the files found (default: only list them); do not run during an import or a publish")
	})
	if err != nil || len(pos) != 0 || f.olderThan < media.MinSweepAge {
		return f, errors.New(mediaUsageErr)
	}
	return f, nil
}

// mediaSweepPlan is everything one `media sweep` run needs; runMediaSweep
// builds it from the environment and tests build it from fakes.
type mediaSweepPlan struct {
	mediaSweepFlags
	store    media.SweepStore
	bucket   string // printed: the resolved bucket/prefix
	loadKeys func(ctx context.Context) (map[string]struct{}, error)
	now      time.Time
}

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
	if len(args) >= 2 && args[0] == "media" && args[1] == "adopt" {
		return runMediaAdopt(ctx, databaseURL, args[2:], out)
	}
	if len(args) >= 2 && args[0] == "media" && args[1] == "sweep" {
		return runMediaSweep(ctx, databaseURL, args[2:], out)
	}
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

// runMediaSweep runs `hatamoto media sweep`.
func runMediaSweep(ctx context.Context, databaseURL string, args []string, out io.Writer) error {
	f, err := parseMediaSweepArgs(args)
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if driver, err := media.StorageFromEnv(os.Getenv); err != nil {
		return err
	} else if driver != "s3" {
		fmt.Fprintln(out, "media sweep needs object storage (MEDIA_STORAGE=s3, HATAMOTO_MEDIA_S3_*); local media has no orphan sweep")
		return exitError{1}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Database.URL = databaseURL
	target, err := media.S3TargetFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	store, err := media.NewS3Store(target, "/media", nil)
	if err != nil {
		return err
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	repo := media.NewPostgresRepository(db)
	p := mediaSweepPlan{mediaSweepFlags: f, store: store, bucket: target.Bucket + "/" + target.Prefix,
		loadKeys: repo.ReferencedKeys}
	return mediaSweep(ctx, p, out)
}

// listAllMedia walks the whole library by id, so a row is never skipped or
// seen twice however the rows are ordered by date or change during the run.
func listAllMedia(ctx context.Context, repo interface {
	ListAllAfter(ctx context.Context, after uuid.UUID, limit int) ([]*media.Media, error)
}) ([]*media.Media, error) {
	const page = 500
	var all []*media.Media
	var after uuid.UUID
	for {
		items, err := repo.ListAllAfter(ctx, after, page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < page {
			return all, nil
		}
		after = items[len(items)-1].ID
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

// sweepAge prints a threshold as 24h when it is whole hours.
func sweepAge(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return d.String()
}

func mediaSweep(ctx context.Context, p mediaSweepPlan, out io.Writer) error {
	res, err := media.Sweep(ctx, p.store, p.loadKeys, media.SweepOptions{OlderThan: p.olderThan, Apply: p.apply, Now: p.now})
	if err != nil {
		return err
	}
	n := len(res.Orphans)
	if !p.apply {
		fmt.Fprintf(out, "dry run: %s orphan objects under %s older than %s (%s)", thousands(n), p.bucket, sweepAge(p.olderThan), humanBytes(res.Bytes))
		if n == 0 {
			fmt.Fprintln(out)
			return nil
		}
		fmt.Fprintln(out, ":")
		for _, o := range res.Orphans[:min(n, maxSweepListed)] {
			fmt.Fprintf(out, "  %s  %s  %s\n", o.Key, humanBytes(o.Size), o.ModTime.UTC().Format(time.RFC3339))
		}
		if n > maxSweepListed {
			fmt.Fprintf(out, "  and %s more\n", thousands(n-maxSweepListed))
		}
		fmt.Fprintln(out, "run with --apply to delete them")
		return nil
	}
	for _, e := range res.Failed {
		fmt.Fprintf(out, "failed: %v\n", e)
	}
	if len(res.Failed) > 0 {
		fmt.Fprintf(out, "deleted %s of %s orphan objects\n", thousands(res.Deleted), thousands(n))
		return exitError{1}
	}
	fmt.Fprintf(out, "deleted %s orphan objects (%s)\n", thousands(res.Deleted), humanBytes(res.Bytes))
	return nil
}

type mediaAdoptFlags struct {
	dir       string
	olderThan time.Duration
	apply     bool
}

func parseMediaAdoptArgs(args []string) (mediaAdoptFlags, error) {
	f := mediaAdoptFlags{olderThan: 24 * time.Hour}
	pos, err := flagSet("media adopt", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&f.olderThan, "older-than", f.olderThan, "only files at least this old")
		fs.BoolVar(&f.apply, "apply", false, "create the records (default: only list the files); do not run during an import or a publish")
	})
	if err != nil || len(pos) > 1 {
		return f, errors.New(mediaUsageErr)
	}
	if len(pos) == 1 {
		f.dir = pos[0]
	}
	if _, err := media.CleanKeyPrefix(f.dir); err != nil {
		return f, errors.New(mediaUsageErr)
	}
	return f, nil
}

// mediaAdoptPlan is everything one `media adopt` run needs; runMediaAdopt
// builds it from the environment and tests build it from fakes.
type mediaAdoptPlan struct {
	mediaAdoptFlags
	repo     media.AdoptRepo
	lister   media.Lister
	provider string // storage_provider of the active store: local or s3
	url      func(key string) string
}

// runMediaAdopt runs `hatamoto media adopt`.
func runMediaAdopt(ctx context.Context, databaseURL string, args []string, out io.Writer) error {
	f, err := parseMediaAdoptArgs(args)
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
	store, err := media.NewStoreFromEnv(os.Getenv, "/media")
	if err != nil {
		return err
	}
	provider := "s3"
	if store == nil {
		provider = "local"
		if store, err = media.NewLocalStore(mediaDirOr(cfg.Server.MediaUploadDir), "/media"); err != nil {
			return err
		}
	}
	lister, ok := store.(media.Lister)
	if !ok {
		return errors.New("the media store cannot list its files")
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	return mediaAdopt(ctx, mediaAdoptPlan{mediaAdoptFlags: f, repo: media.NewPostgresRepository(db), lister: lister, provider: provider, url: store.URL}, out)
}

func mediaAdopt(ctx context.Context, p mediaAdoptPlan, out io.Writer) error {
	res, err := media.Adopt(ctx, p.repo, p.lister, media.AdoptOptions{Dir: p.dir, Apply: p.apply, OlderThan: p.olderThan, URL: p.url, Provider: p.provider})
	if err != nil {
		return err
	}
	n := len(res.Items)
	if !p.apply {
		if n == 0 {
			fmt.Fprintln(out, "no files without a media record")
		} else {
			fmt.Fprintf(out, "would adopt %s files (dry run; --apply to create the rows):\n", thousands(n))
		}
		for _, it := range res.Items[:min(n, maxSweepListed)] {
			fmt.Fprintf(out, "  %s  %s  %s  folder %s\n", media.DisplayKey(it.Key), it.Mime, humanBytes(it.Size), media.DisplayKey(it.Folder))
		}
		if n > maxSweepListed {
			fmt.Fprintf(out, "  and %s more\n", thousands(n-maxSweepListed))
		}
	} else {
		fmt.Fprintf(out, "adopted %s files (rows created, no variants)\n", thousands(res.Adopted))
	}
	if res.AlreadyHaveRow > 0 {
		fmt.Fprintf(out, "skipped: %s already have a row\n", thousands(res.AlreadyHaveRow))
	}
	if res.TooNew > 0 {
		fmt.Fprintf(out, "skipped: %s files younger than %s (too new; they may still be in flight, run again later)\n", thousands(res.TooNew), sweepAge(p.olderThan))
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(out, "skipped: %s\n", s)
	}
	for _, it := range res.Items {
		if it.Mime == "application/octet-stream" {
			fmt.Fprintln(out, "note: files of other types are adopted as application/octet-stream; a content archive import does not restore them")
			break
		}
	}
	return nil
}
