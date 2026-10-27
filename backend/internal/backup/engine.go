package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/portability"
)

// Environment variables that set how many ok backups of a kind are kept.
const (
	EnvKeep          = "HATAMOTO_BACKUP_KEEP"
	EnvKeepPreUpdate = "HATAMOTO_BACKUP_KEEP_PRE_UPDATE"

	defaultKeep          = 5
	defaultKeepPreUpdate = 2
)

// KeepFromEnv reads the retention counts (manual, pre-update); a missing or
// invalid value gives the default of 5 and 2.
func KeepFromEnv() (manual, preUpdate int) {
	return keepEnv(EnvKeep, defaultKeep), keepEnv(EnvKeepPreUpdate, defaultKeepPreUpdate)
}

func keepEnv(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n >= 1 {
		return n
	}
	return def
}

// Options configure an Engine.
type Options struct {
	Data        string // the data dir; backups live in $Data/backups
	MediaDir    string // upload dir of the content export; empty means ./uploads
	DatabaseURL string
	Pool        *pgxpool.Pool // nil means the database is not reachable
	Run         ExecFunc      // nil means OSExec
	// Free reports the bytes writable under dir; nil skips the space check.
	Free          func(dir string) (uint64, error)
	Now           func() time.Time
	Log           *slog.Logger
	Version       string // hatamoto version, recorded in the manifest
	KeepManual    int
	KeepPreUpdate int
	// SecretsDir holds secrets.env; the file is added to a backup when present.
	SecretsDir string
	// ConfigPath is the operator's env file, stored verbatim; its path is
	// recorded in the manifest as an absolute path. Empty means no
	// config part; a Request.ConfigPath wins.
	ConfigPath string
	// Trust gives the key IDs recorded in the manifest; nil means the IDs
	// embedded in this binary.
	Trust func() (TrustIDs, error)
}

// Request asks for one backup.
type Request struct {
	Kind   Kind
	Target string        // the version a pre-update backup protects
	Wait   time.Duration // how long a pre-update backup waits for the lock; 0 means DefaultWait
	// ConfigPath overrides Options.ConfigPath for this backup.
	ConfigPath string
}

type exportFunc func(ctx context.Context, w io.Writer, opts portability.ExportOptions) (*portability.Manifest, error)

// Engine takes backups and manages the ones on disk.
type Engine struct {
	opts        Options
	run         ExecFunc
	databaseURL string
	serverMajor func(ctx context.Context) (int, error)
	q           Querier // nil when the database is not reachable
	exportFn    exportFunc
	store       *Store
	storeErr    error
}

// New builds an Engine. A data dir that cannot be created shows up as an error
// from every call that needs it.
func New(o Options) *Engine {
	if o.Run == nil {
		o.Run = OSExec
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Trust == nil {
		o.Trust = embeddedTrust
	}
	if o.KeepManual < 1 {
		o.KeepManual = defaultKeep
	}
	if o.KeepPreUpdate < 1 {
		o.KeepPreUpdate = defaultKeepPreUpdate
	}
	e := &Engine{opts: o, run: o.Run, databaseURL: o.DatabaseURL}
	e.store, e.storeErr = NewStore(o.Data)
	if o.Pool != nil {
		pool := o.Pool
		e.q = pool
		e.exportFn = func(ctx context.Context, w io.Writer, opts portability.ExportOptions) (*portability.Manifest, error) {
			return portability.Export(ctx, pool, w, opts)
		}
	}
	e.serverMajor = func(ctx context.Context) (int, error) {
		var n int
		err := e.q.QueryRow(ctx, `SELECT current_setting('server_version_num')::int / 10000`).Scan(&n)
		return n, err
	}
	return e
}

func (e *Engine) st() (*Store, error) {
	if e.storeErr != nil {
		return nil, fmt.Errorf("backup directory: %w", e.storeErr)
	}
	return e.store, nil
}

// List, Get, Delete and Open need only the backups directory.
func (e *Engine) List() ([]Info, error) {
	s, err := e.st()
	if err != nil {
		return nil, err
	}
	return s.List()
}

func (e *Engine) Get(id string) (Info, error) {
	s, err := e.st()
	if err != nil {
		return Info{}, err
	}
	return s.Get(id)
}

func (e *Engine) Delete(id string) error {
	s, err := e.st()
	if err != nil {
		return err
	}
	return s.Delete(id)
}

func (e *Engine) Open(id string) (*os.File, Info, error) {
	s, err := e.st()
	if err != nil {
		return nil, Info{}, err
	}
	return s.Open(id)
}

// FreeDisk is the free space of the backups directory, 0 when unknown.
func (e *Engine) FreeDisk() uint64 {
	s, err := e.st()
	if err != nil || e.opts.Free == nil {
		return 0
	}
	n, err := e.opts.Free(s.Dir())
	if err != nil {
		return 0
	}
	return n
}

// Create takes a backup and returns its record. A manual request while another
// backup runs gives ErrBusy at once; a pre-update request waits.
func (e *Engine) Create(ctx context.Context, req Request) (Info, error) {
	_, work, err := e.Begin(ctx, req)
	if err != nil {
		return Info{}, err
	}
	return work(ctx)
}

// Begin takes the lock and writes the running record before it returns, so a
// caller can answer "started" (or ErrBusy) at once and do the work later. The
// returned function does the work and releases the lock; it must be called
// exactly once. Pass it a lifecycle context, never a request context.
func (e *Engine) Begin(ctx context.Context, req Request) (Info, func(ctx context.Context) (Info, error), error) {
	s, err := e.st()
	if err != nil {
		return Info{}, nil, err
	}
	var release func()
	if req.Kind == KindPreUpdate {
		wait := req.Wait
		if wait <= 0 {
			wait = DefaultWait
		}
		release, err = WaitLock(ctx, s.Dir(), wait)
	} else {
		release, err = TryLock(s.Dir())
	}
	if err != nil {
		return Info{}, nil, err
	}
	created := e.opts.Now().UTC().Truncate(time.Second)
	for { // two backups of one kind in one second must not share an id
		if _, err := os.Stat(s.sidecar(NewID(created, req.Kind))); errors.Is(err, fs.ErrNotExist) {
			break
		}
		created = created.Add(time.Second)
	}
	info := Info{ID: NewID(created, req.Kind), Kind: req.Kind, Target: req.Target,
		State: StateRunning, Step: "starting", Created: created}
	if err := s.Write(info); err != nil {
		release()
		return Info{}, nil, err
	}
	return info, func(ctx context.Context) (Info, error) {
		defer release()
		return e.run1(ctx, s, info, req)
	}, nil
}

// run1 does the work of one backup and records the outcome.
func (e *Engine) run1(ctx context.Context, s *Store, info Info, req Request) (Info, error) {
	work := filepath.Join(s.Dir(), tmpPrefix+info.ID)
	done, err := e.build(ctx, s, &info, work, req)
	_ = os.RemoveAll(work)
	if err != nil {
		info.State, info.Step, info.Error = StateFailed, "", err.Error()
		if werr := s.Write(info); werr != nil {
			e.opts.Log.Error("backup: cannot record failure", "id", info.ID, "error", werr)
		}
		e.opts.Log.Error("backup failed", "id", info.ID, "error", err)
		return info, err
	}
	info = done
	if err := s.Write(info); err != nil {
		return info, err
	}
	keep := e.opts.KeepManual
	if info.Kind == KindPreUpdate {
		keep = e.opts.KeepPreUpdate
	}
	if info.Kind == KindManual || info.Kind == KindPreUpdate {
		if err := s.Prune(info.Kind, keep); err != nil {
			e.opts.Log.Warn("backup: retention failed", "error", err)
		}
	}
	return info, nil
}

// progress records a finished step.
type progress struct {
	s     *Store
	info  *Info
	done  int
	total int
	log   *slog.Logger
}

func (p *progress) step(name string) {
	p.info.Step = name
	if p.total > 0 {
		p.info.Percent = p.done * 100 / p.total
	}
	if err := p.s.Write(*p.info); err != nil {
		p.log.Warn("backup: cannot record progress", "id", p.info.ID, "error", err)
	}
}

func (p *progress) finished() { p.done++ }

func (e *Engine) build(ctx context.Context, s *Store, info *Info, work string, req Request) (Info, error) {
	pr := &progress{s: s, info: info, log: e.opts.Log}
	pr.step("checking")
	if e.q == nil {
		return *info, errors.New("database not reachable")
	}
	if err := e.preflight(ctx, s.Dir()); err != nil {
		return *info, err
	}
	pgVersion, err := e.checkTool(ctx)
	if err != nil {
		return *info, err
	}
	plugins, err := discover(ctx, e.q)
	if err != nil {
		return *info, err
	}
	pr.total = len(plugins) + 4 // preflight, core, plugins, content, finish
	pr.finished()
	if err := os.MkdirAll(work, 0o700); err != nil {
		return *info, err
	}

	var parts []part
	add := func(name string) error {
		p, err := describe(work, name)
		if err != nil {
			return err
		}
		parts = append(parts, p)
		return nil
	}

	pr.step("core database")
	if err := e.dumpCore(ctx, filepath.Join(work, "core.dump"), plugins); err != nil {
		return *info, err
	}
	if err := add("core.dump"); err != nil {
		return *info, err
	}
	pr.finished()

	var pluginNames []string
	for _, p := range plugins {
		pr.step("plugin " + p.Name)
		if err := e.dumpPlugin(ctx, filepath.Join(work, p.Part()), p); err != nil {
			return *info, err
		}
		if err := add(p.Part()); err != nil {
			return *info, err
		}
		pluginNames = append(pluginNames, p.Name)
		pr.finished()
	}

	pr.step("secrets and config")
	configPath := req.ConfigPath
	if configPath == "" {
		configPath = e.opts.ConfigPath
	}
	if configPath != "" { // recorded absolute, so a restore on another machine can show it as a hint
		if configPath, err = filepath.Abs(configPath); err != nil {
			return *info, fmt.Errorf("config file: %w", err)
		}
	}
	extra, err := e.addOwnerFiles(work, configPath)
	if err != nil {
		return *info, err
	}
	for _, name := range extra {
		if err := add(name); err != nil {
			return *info, err
		}
	}

	pr.step("content")
	man, err := e.exportContent(ctx, filepath.Join(work, "content.tar.gz"))
	if err != nil {
		return *info, err
	}
	if err := add("content.tar.gz"); err != nil {
		return *info, err
	}
	pr.finished()

	pr.step("finishing")
	server, _ := e.serverMajor(ctx)
	trust, err := e.opts.Trust()
	if err != nil {
		return *info, fmt.Errorf("trust keys: %w", err)
	}
	mf := manifest{
		Format: manifestFormat, Version: manifestVersion, Created: info.Created,
		Kind: info.Kind, Target: info.Target, HatamotoVersion: e.opts.Version,
		ServerMajor: server, PgDump: pgVersion, Parts: parts, Plugins: pluginNames,
		ContentRows: man.Rows, MediaFiles: man.MediaFiles, MediaBytes: man.MediaBytes, MediaMissing: man.MediaMissing,
		Trust: &trust, ConfigPath: configPath,
	}
	size, names, err := writeArchive(s, info.ID, work, parts, mf)
	if err != nil {
		return *info, err
	}
	done := *info
	done.State, done.Step, done.Percent, done.Error = StateOK, "", 100, ""
	done.Size, done.Parts, done.Plugins, done.MediaMissing = size, names, pluginNames, man.MediaMissing
	return done, nil
}

// addOwnerFiles copies secrets.env (when the secrets dir has one) and the
// config file (when a path is set) into work and returns the part names. A
// named config file that cannot be read fails the backup: it was asked for.
func (e *Engine) addOwnerFiles(work, configPath string) ([]string, error) {
	var names []string
	if e.opts.SecretsDir != "" {
		err := copyPart(filepath.Join(e.opts.SecretsDir, secretsName), filepath.Join(work, secretsName))
		switch {
		case err == nil:
			names = append(names, secretsName)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("secrets: %w", err)
		}
	}
	if configPath != "" {
		if err := copyPart(configPath, filepath.Join(work, configName)); err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		names = append(names, configName)
	}
	return names, nil
}

// copyPart copies src to a new 0600 file dst.
func copyPart(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- operator supplied path
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// maxMissingLogged caps the missing media paths named in the log.
const maxMissingLogged = 10

func (e *Engine) exportContent(ctx context.Context, path string) (*portability.Manifest, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	var missing []string
	man, err := e.exportFn(ctx, f, portability.ExportOptions{MediaDir: e.mediaDir(), OnMissing: func(rel string) {
		if len(missing) < maxMissingLogged {
			missing = append(missing, rel)
		}
	}})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("content export: %w", err)
	}
	if man.MediaMissing > 0 {
		dir := e.MediaDirAbs()
		if st, serr := os.Stat(dir); serr != nil || !st.IsDir() {
			return nil, fmt.Errorf("media directory %s not found; set MEDIA_UPLOAD_DIR or run from the service directory", dir)
		}
		if man.MediaFiles == 0 {
			return nil, fmt.Errorf("none of the %d media files referenced by the database were found in %s; set MEDIA_UPLOAD_DIR or run from the service directory", man.MediaMissing, dir)
		}
		e.opts.Log.Warn("backup: media files referenced by the database are missing and left out",
			"dir", dir, "missing", man.MediaMissing, "sample", missing)
	}
	return man, nil
}

// MediaDirAbs is the absolute path of the media dir the content export reads.
func (e *Engine) MediaDirAbs() string {
	dir, err := filepath.Abs(e.mediaDir())
	if err != nil {
		return e.mediaDir()
	}
	return dir
}

func (e *Engine) mediaDir() string {
	if e.opts.MediaDir == "" {
		return "./uploads"
	}
	return e.opts.MediaDir
}

// backupsDirMargin is slack on top of the backups directory's peak use.
const backupsDirMargin = 64 << 20

// preflight refuses to start when the backup would not fit. The backups
// directory holds the dump parts and the finished tar side by side, so its
// peak is about twice the database plus media, with a margin. The temp dir
// only holds the content export spool: half again as headroom.
func (e *Engine) preflight(ctx context.Context, dir string) error {
	if e.opts.Free == nil {
		return nil
	}
	var db int64
	if err := e.q.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&db); err != nil {
		return fmt.Errorf("cannot read the database size: %w", err)
	}
	size := uint64(db) + dirBytes(e.mediaDir())
	for _, c := range []struct {
		dir  string
		need uint64
	}{{dir, size*2 + backupsDirMargin}, {os.TempDir(), size * 3 / 2}} {
		free, err := e.opts.Free(c.dir)
		if err != nil {
			return fmt.Errorf("cannot read free disk space of %s: %w", c.dir, err)
		}
		if free < c.need {
			return fmt.Errorf("not enough free disk space: need %d MB, have %d MB in %s", mb(c.need), free>>20, c.dir)
		}
	}
	return nil
}

// mb rounds up, so a backup that needs 1 byte never reads "need 0 MB".
func mb(n uint64) uint64 { return (n + 1<<20 - 1) >> 20 }

func dirBytes(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a missing or unreadable upload dir counts as empty
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += uint64(fi.Size())
			}
		}
		return nil
	})
	return n
}
