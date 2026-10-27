package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/supervisor"
	"hatamoto.dev/core/internal/updater"
)

// updateArgs is a parsed `update` command line.
type updateArgs struct {
	verb              string // apply (files), rollback, status, check, pin, unpin, fetch (apply vX.Y.Z)
	rollback          bool
	force             bool
	noBackup          bool // apply vX.Y.Z: skip the pre-update backup for this run
	dryRun            bool
	pin               string // pin vX.Y
	version           string // apply vX.Y.Z
	file, record, sig string
}

const updateUsage = "usage: hatamoto update status | check [--dry-run] | pin vX.Y | unpin | apply vX.Y.Z [--force] [--no-backup] | " +
	"apply --file F --record R [--sig S] | rollback [--force]"

var pinArg = regexp.MustCompile(`^v\d+\.\d+$`)

// parseUpdateArgs parses what follows "update".
func parseUpdateArgs(args []string) (updateArgs, error) {
	var u updateArgs
	if len(args) == 0 {
		return u, errors.New(updateUsage)
	}
	u.verb = args[0]
	switch args[0] {
	case "status", "unpin":
		if len(args) != 1 {
			return u, fmt.Errorf("usage: hatamoto update %s", args[0])
		}
		return u, nil
	case "check":
		for _, a := range args[1:] {
			if a != "--dry-run" {
				return u, errors.New("usage: hatamoto update check [--dry-run]")
			}
			u.dryRun = true
		}
		return u, nil
	case "pin":
		if len(args) != 2 || !pinArg.MatchString(args[1]) {
			return u, errors.New("usage: hatamoto update pin vMAJOR.MINOR (for example v1.4)")
		}
		u.pin = args[1]
		return u, nil
	case "rollback":
		u.rollback = true
		for _, a := range args[1:] {
			if a != "--force" {
				return u, errors.New("usage: hatamoto update rollback [--force]")
			}
			u.force = true
		}
		return u, nil
	case "apply":
		if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
			if !release.ValidVersion(args[1]) {
				return u, fmt.Errorf("%q is not a release version (for example v1.4.2)", args[1])
			}
			u.verb, u.version = "fetch", args[1]
			for _, a := range args[2:] {
				switch a {
				case "--force":
					u.force = true
				case "--no-backup":
					u.noBackup = true
				default:
					return u, errors.New("usage: hatamoto update apply vMAJOR.MINOR.PATCH [--force] [--no-backup]")
				}
			}
			return u, nil
		}
		fs := flag.NewFlagSet("update apply", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.StringVar(&u.file, "file", "", "release binary")
		fs.StringVar(&u.record, "record", "", "signed release record")
		fs.StringVar(&u.sig, "sig", "", "record signature file (default <record>.sig)")
		if err := fs.Parse(args[1:]); err != nil {
			return u, fmt.Errorf("update apply: %w", err)
		}
		if u.file == "" || u.record == "" || fs.NArg() != 0 {
			return u, errors.New("usage: hatamoto update apply --file F --record R [--sig S] | hatamoto update apply vX.Y.Z [--force] [--no-backup]")
		}
		return u, nil
	}
	return u, errors.New(updateUsage)
}

// runUpdate runs `hatamoto update ...`.
func runUpdate(ctx context.Context, args []string, out io.Writer) error {
	return runUpdateCtx(ctx, args, out, release.Options{})
}

// runUpdateWith runs the verbs that need no network or database (apply from
// files, rollback) with the trust sets injected (tests use throwaway keys).
func runUpdateWith(args []string, out io.Writer, ro release.Options) error {
	return runUpdateCtx(context.Background(), args, out, ro)
}

func runUpdateCtx(ctx context.Context, args []string, out io.Writer, ro release.Options) error {
	u, err := parseUpdateArgs(args)
	if err != nil {
		return err
	}
	cfg, err := superviseConfig(nil)
	if err != nil {
		return err
	}
	if err := requireKnownDataDir(cfg.Data); err != nil {
		return err
	}
	cfg.Release = ro
	switch u.verb {
	case "status", "check", "pin", "unpin", "fetch":
		return runUpdateOnline(ctx, u, cfg, out)
	}
	var res supervisor.Result
	if u.rollback {
		cfg.Force = u.force
		res, err = supervisor.Rollback(cfg)
	} else {
		res, err = supervisor.Apply(cfg, supervisor.ApplyOpts{File: u.file, Record: u.record, Sig: u.sig, Release: ro})
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, res.Message)
	fmt.Fprintln(out, "releases dir:", filepath.Join(cfg.Data, "releases"))
	return nil
}

// checkOwner refuses to run as a user other than the data dir's owner (root
// would leave files the service user cannot read). A variable so tests can
// stand in for a foreign owner.
var checkOwner = supervisor.CheckOwner

// takesBackup reports whether the run backs up first: both verbs that can
// install (check and apply vX.Y.Z) do, unless skipped or a dry run.
func takesBackup(u updateArgs) bool {
	return (u.verb == "fetch" || u.verb == "check") && !u.noBackup && !u.dryRun
}

// runUpdateOnline runs the verbs that read the update settings (a database)
// or the network. `apply vX.Y.Z` touches the database only for the plugin
// preflight, and only when DATABASE_URL is set.
func runUpdateOnline(ctx context.Context, u updateArgs, cfg supervisor.Config, out io.Writer) error {
	if u.verb != "status" { // everything else may create install_id, backups/ or releases/ files
		if err := checkOwner(supervisor.NewLayout(cfg.Data)); err != nil {
			return err
		}
	}
	version := runningVersionOf(cfg)
	env := updater.Env{Data: cfg.Data, Version: cfg.BootstrapVersion, Supervisor: cfg, BaseURL: os.Getenv("HATAMOTO_UPDATE_URL"),
		DatabaseURL: os.Getenv("DATABASE_URL"), Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	withDB := u.verb != "fetch" || (os.Getenv("DATABASE_URL") != "" && !u.force)
	// The backup needs the database even when --force skips the plugin preflight.
	wantBackup := takesBackup(u)
	if withDB || (wantBackup && os.Getenv("DATABASE_URL") != "") {
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if withDB {
			env.Store = updater.NewStore(db.Pool())
			env.Plugins = server.UpdatePlugins(packages.NewStore(db.Pool()))
			env.Imports = importProbe{db.Pool()}
			env.Site = siteLocation(db.Pool())
		}
		if wantBackup {
			c, err := config.Load()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			env.Backups = preUpdateBackups{newBackupEngine(db.Pool(), c.Server.MediaUploadDir, version, env.Log)}
		}
	}
	if wantBackup && env.Backups == nil {
		env.Backups = noDatabaseBackups{}
	}
	switch u.verb {
	case "status":
		set, st, err := env.Store.Load(ctx)
		if err != nil {
			return err
		}
		renderUpdateStatus(out, version, set.Effective(os.Getenv), st)
		return nil
	case "pin", "unpin":
		set, _, err := env.Store.Load(ctx)
		if err != nil {
			return err
		}
		set.Pin = u.pin // "" for unpin
		if err := env.Store.SaveSettings(ctx, set); err != nil {
			return err
		}
		if u.pin == "" {
			fmt.Fprintln(out, "unpinned")
		} else {
			fmt.Fprintf(out, "pinned to %s (security releases still apply inside the major)\n", u.pin)
		}
		return nil
	}
	if u.verb == "fetch" && env.Store != nil {
		env.Store = nil // the explicit route does not touch the stored status
	}
	up, err := updater.New(env)
	if err != nil {
		return err
	}
	run := updater.RunOpts{DryRun: u.dryRun, Force: u.force, Apply: u.version, SkipBackup: u.noBackup}
	st, err := up.Run(ctx, run)
	if err != nil {
		return err
	}
	if err := updateRefusal(u, st); err != nil {
		return err
	}
	set := updater.Settings{}.Effective(os.Getenv)
	if env.Store != nil {
		set, _, _ = env.Store.Load(ctx)
		set = set.Effective(os.Getenv)
	}
	renderUpdateStatus(out, version, set, st)
	return nil
}

// updateRefusal turns an update that did not install into a non-zero exit. A
// failed pre-update backup blocks `apply` and a `check` that would install
// alike, so scripts do not mistake it for success.
func updateRefusal(u updateArgs, st updater.Status) error {
	switch {
	case st.Decision == updater.KindBackupFailed:
		target := st.Target
		if target == "" {
			target = "the update"
		}
		return fmt.Errorf("not installing %s: the pre-update backup failed: %s; fix that, or install without a backup with --no-backup (HATAMOTO_UPDATE_BACKUP=0 turns it off for every update)", target, st.Reason)
	case u.verb == "fetch" && st.LastResult == updater.ResultSkipped:
		return fmt.Errorf("not installing %s now: %s", u.version, st.Reason)
	}
	return nil
}

// runningVersionOf is the version the supervisor runs on this box: the current
// release once a self-update has happened, else this binary's own build. This
// process is the bootstrap binary, which is no longer what runs after an update.
func runningVersionOf(cfg supervisor.Config) string {
	st, err := supervisor.LoadState(supervisor.NewLayout(cfg.Data).StatePath())
	if err != nil {
		return cfg.BootstrapVersion
	}
	return supervisor.RunningVersion(supervisor.NewLayout(cfg.Data), st, cfg.BootstrapVersion)
}

// renderUpdateStatus prints the settings and the last run's outcome.
func renderUpdateStatus(out io.Writer, version string, set updater.Settings, st updater.Status) {
	if version == "" {
		version = "dev"
	}
	fmt.Fprintf(out, "version     %s (channel %s, mode %s)\n", version, set.Channel, set.Mode)
	if set.Pin != "" {
		fmt.Fprintf(out, "pin         %s\n", set.Pin)
	}
	switch {
	case st.Decision == "":
		fmt.Fprintln(out, "status      no check yet")
	case st.Decision == updater.KindRolledBack:
		fmt.Fprintf(out, "status      rolled back %s: %s\n", st.Target, st.Reason)
	case st.Reason == "":
		fmt.Fprintf(out, "status      %s\n", strings.ReplaceAll(st.Decision, "_", " "))
	default:
		fmt.Fprintf(out, "status      %s: %s\n", strings.ReplaceAll(st.Decision, "_", " "), st.Reason)
	}
	if st.AvailableMajor != "" {
		fmt.Fprintf(out, "major       %s is available; install it with `hatamoto update apply %s`\n", st.AvailableMajor, st.AvailableMajor)
	}
	if st.LastCheckAt == nil {
		fmt.Fprintln(out, "last check  never")
		return
	}
	fmt.Fprintf(out, "last check  %s %s\n", st.LastCheckAt.UTC().Format(time.RFC3339), st.LastResult)
}

// siteLocation reads the site timezone for the maintenance window; UTC when unset.
func siteLocation(pool *pgxpool.Pool) func(context.Context) *time.Location {
	return func(ctx context.Context) *time.Location {
		var tz *string
		if err := pool.QueryRow(ctx, `SELECT timezone FROM hatamoto_site_config LIMIT 1`).Scan(&tz); err != nil || tz == nil {
			return time.UTC
		}
		if loc, err := time.LoadLocation(*tz); err == nil {
			return loc
		}
		return time.UTC
	}
}

// requireKnownDataDir stops the CLI from quietly working on ./data of
// whatever directory the operator happens to be in. With HATAMOTO_DATA unset
// the default data dir is relative to the cwd, so it has to be one where a
// supervisor already keeps its state; otherwise the message says how to
// point at the right one.
func requireKnownDataDir(data string) error {
	if strings.TrimSpace(os.Getenv("HATAMOTO_DATA")) != "" {
		return nil
	}
	for _, f := range []string{"state.json", "supervisor.pid"} {
		if _, err := os.Lstat(filepath.Join(data, "releases", f)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no supervisor state under %s (HATAMOTO_DATA is not set, so this is ./data of the current directory). "+
		"Run from the service's working directory (cd /opt/hatamoto) or set HATAMOTO_DATA to the service's data dir", data)
}
