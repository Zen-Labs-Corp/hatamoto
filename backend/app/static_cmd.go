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
	"strings"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/staticexport"
)

const staticUsageErr = "usage: hatamoto static export [--out DIR] [--target dir|s3]"

const staticUsage = `  static export [--out DIR] [--target dir|s3]
                                   render the public site to plain files in a directory or an S3 bucket (see docs/STATIC_EXPORT.md; not the archive "export")`

type staticArgs struct {
	out    string
	target string // "", "dir" or "s3"
}

// parseStaticArgs reads the arguments after "static export".
func parseStaticArgs(args []string) (staticArgs, error) {
	var a staticArgs
	pos, err := flagSet("static export", args, func(fs *flag.FlagSet) {
		fs.StringVar(&a.out, "out", "", "directory to publish to (implies --target dir)")
		fs.StringVar(&a.target, "target", "", "dir or s3 (default: the saved hosting settings)")
	})
	if err != nil || len(pos) != 0 {
		return a, errors.New(staticUsageErr)
	}
	if a.target != "" && a.target != staticexport.TargetDir && a.target != staticexport.TargetS3 {
		return a, errors.New(staticUsageErr)
	}
	if a.out != "" {
		if a.target == staticexport.TargetS3 {
			return a, errors.New("static export: --out and --target s3 cannot be combined")
		}
		a.target = staticexport.TargetDir
	}
	return a, nil
}

// staticTargetDeps are the inputs resolveStaticTarget reads, so tests can
// stand in for the database, the environment and the server secret.
type staticTargetDeps struct {
	getenv    func(string) string
	settings  func(context.Context) (staticexport.Settings, bool, error)
	secret    func() (string, error)
	forbidden []string // directories an export must never replace
}

// resolveStaticTarget picks the publish target: --out, the HATAMOTO_STATIC_S3_*
// environment, or the saved hosting settings.
func resolveStaticTarget(ctx context.Context, a staticArgs, d staticTargetDeps) (staticexport.Target, error) {
	if a.out != "" {
		return staticexport.NewDirTarget(a.out, staticexport.DirGuard{Forbidden: d.forbidden})
	}
	if a.target == staticexport.TargetS3 {
		if t, ok, err := staticexport.S3TargetFromEnv(d.getenv); err != nil {
			return nil, err
		} else if ok {
			return s3StaticTarget(t.Endpoint, t.Region, t.Bucket, t.Prefix, t.AccessKey, t.SecretKey, d.getenv)
		}
	}
	set, found, err := d.settings(ctx)
	if err != nil {
		return nil, err
	}
	target := a.target
	if target == "" {
		if !found || set.Mode != staticexport.ModeStatic {
			return nil, errors.New("no target: pass --out DIR or --target s3 (or save the hosting settings)")
		}
		target = set.Target
	}
	if target == staticexport.TargetDir {
		if set.DirPath == "" {
			return nil, errors.New("no directory saved: pass --out DIR")
		}
		return staticexport.NewDirTarget(set.DirPath, staticexport.DirGuard{Forbidden: d.forbidden})
	}
	if !found || set.Bucket == "" {
		return nil, fmt.Errorf("no S3 target: set %s and the other %s variables, or save the hosting settings", staticexport.EnvBucket, "HATAMOTO_STATIC_S3_*")
	}
	secret, err := d.secret()
	if err != nil {
		return nil, fmt.Errorf("cannot open the saved S3 secret key: %w", err)
	}
	t, err := set.S3Target(staticexport.SecretKey(secret))
	if err != nil {
		return nil, err
	}
	return s3StaticTarget(t.Endpoint, t.Region, t.Bucket, t.Prefix, t.AccessKey, t.SecretKey, d.getenv)
}

// s3StaticTarget builds the S3 target and refuses one that overlaps the media
// object storage (/media serves everything under the media prefix).
func s3StaticTarget(endpoint, region, bucket, prefix, accessKey, secretKey string, getenv func(string) string) (staticexport.Target, error) {
	if kind, _ := media.StorageFromEnv(getenv); kind == "s3" {
		if mt, err := media.S3TargetFromEnv(getenv); err == nil && mt.Overlaps(endpoint, bucket, prefix) {
			return nil, errors.New("the static export target and the media object storage share a bucket and overlapping prefixes; use a separate bucket or distinct prefixes")
		}
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	c, err := offsite.Target{Endpoint: endpoint, Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey}.Client(nil)
	if err != nil {
		return nil, err
	}
	return staticexport.NewS3Target(c, endpoint, bucket, prefix), nil
}

// exportServerSecret puts the secret a server would use into the environment
// for config.Load: from HATAMOTO_SECRET or JWT_SECRET, else secrets.env in the
// secrets directory. It reads only; it never generates a secret.
func exportServerSecret() error {
	v, err := serverSecret()
	if err != nil {
		return fmt.Errorf("read the server secret: %w", err)
	}
	if v == "" {
		return fmt.Errorf("no server secret: set %s or run the server once so it creates %s", secretEnv, filepath.Join(secretsDirFromEnv(), "secrets.env"))
	}
	return os.Setenv(secretEnv, v)
}

// runStatic runs `hatamoto static export`: it renders the site through the
// router in this process (it never starts a listener) and publishes it.
func runStatic(ctx context.Context, args []string, out io.Writer) error {
	a, err := parseStaticArgs(args)
	if err != nil {
		return err
	}
	// The worker must not start for a one-shot render.
	if err := os.Setenv("HATAMOTO_WORKER", "off"); err != nil {
		return err
	}
	if err := exportServerSecret(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	cfg.Server.EnableWorker = false
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	// The export never migrates: rendering needs the schema this binary expects.
	pool, closeDirect, err := db.Direct().Pool(ctx, 2)
	if err != nil {
		return err
	}
	var pending bool
	err = withProvider(pool, func(p *goose.Provider) error {
		var herr error
		pending, herr = p.HasPending(ctx)
		return herr
	})
	closeDirect()
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if pending {
		return errors.New("database schema is behind this binary: run `hatamoto migrate` first")
	}

	admin, err := staticAdminPath(ctx, cfg, adminpath.NewStore(db.Pool()))
	if err != nil {
		return err
	}
	cfg.Admin.Resolved = admin
	if err := config.CheckPrefixAgainstAdmin(cfg.PublicSite.APIPrefix, admin); err != nil {
		return err
	}
	useNeutralCookieNames(cfg.AdminCookieSecure)

	store := staticexport.NewSettingsStore(db.Pool())
	t, err := resolveStaticTarget(ctx, a, staticTargetDeps{
		getenv:    os.Getenv,
		settings:  store.Get,
		secret:    serverSecret,
		forbidden: []string{config.ResolveDataDir(), cfg.Server.MediaUploadDir},
	})
	if err != nil {
		return err
	}

	logger := slog.New(noSetupCode{slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})})
	srv := server.New(cfg, db, logger) // never Start()ed: no listener, no worker
	_, err = srv.StaticExport(ctx, t, staticexport.Options{Trigger: "cli", Out: out})
	switch {
	case errors.Is(err, staticexport.ErrBusy):
		fmt.Fprintln(out, "static: another export is running")
		return exitError{1}
	case err != nil:
		fmt.Fprintln(out, exportFailedLine(t))
		return exitError{1}
	}
	return nil
}

// exportFailedLine says what a failed export left behind. A directory swap
// either happens or not, and an S3 failure before the first upload changes
// nothing; after an upload the bucket is partly updated (nothing is deleted
// before every upload has finished).
func exportFailedLine(t staticexport.Target) string {
	if m, ok := t.(interface{ Modified() bool }); ok && m.Modified() {
		return fmt.Sprintf("static: export failed, %s partially updated (no deletes)", t.Describe())
	}
	return fmt.Sprintf("static: export failed, %s unchanged", t.Describe())
}

// staticAdminPath reads the address the admin is served at without writing:
// the HATAMOTO_ADMIN_PATH pin, else the stored one, else the default (no
// address is stored until the server first starts).
func staticAdminPath(ctx context.Context, cfg *config.Config, st interface {
	Get(context.Context) (adminpath.Stored, error)
}) (string, error) {
	if pin := strings.TrimSpace(cfg.Admin.Path); pin != "" {
		p, err := adminpath.Normalize(pin)
		if err != nil {
			return "", fmt.Errorf("HATAMOTO_ADMIN_PATH %q is not valid: %w", pin, err)
		}
		return p, nil
	}
	cur, err := st.Get(ctx)
	if errors.Is(err, adminpath.ErrNoSiteConfig) {
		return defaultAdminAddress, nil
	}
	if err != nil {
		return "", fmt.Errorf("read admin address: %w", err)
	}
	if cur.Path == nil {
		return defaultAdminAddress, nil
	}
	return *cur.Path, nil
}

// noSetupCode drops the first-run setup code notice: server.New logs a fresh
// code (a secret) on a site without users, and a one-shot export is no place
// to print it.
type noSetupCode struct{ slog.Handler }

func (h noSetupCode) Handle(ctx context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "Setup:") {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

func (h noSetupCode) WithAttrs(a []slog.Attr) slog.Handler {
	return noSetupCode{h.Handler.WithAttrs(a)}
}
func (h noSetupCode) WithGroup(n string) slog.Handler { return noSetupCode{h.Handler.WithGroup(n)} }
