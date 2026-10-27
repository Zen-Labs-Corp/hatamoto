// Package importjob runs the WordPress import of the admin wizard: a scan and
// an import as background jobs on the server's process, their state in
// hatamoto_import_runs so a restarted server can resume them, and the source
// credential sealed at rest until the run completes.
//
// The private-host override of the engine (wpimport.Options.AllowPrivate) is
// never offered here: a web request cannot set it. Manager.AllowPrivate exists
// for tests and is never assigned by the server.
package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/wpimport"
)

// MaxWXRBytes caps an uploaded export file (the engine's own cap is 512 MB).
const MaxWXRBytes = 256 << 20

// Statuses as the wizard shows them: the database's, plus "interrupted" for a
// run that says it is running but has no job in this process (a restart).
const (
	StatusScanning    = "scanning"
	StatusScanned     = wpimport.RunScanned
	StatusRunning     = wpimport.RunRunning
	StatusCompleted   = wpimport.RunCompleted
	StatusFailed      = wpimport.RunFailed
	StatusCanceled    = "canceled"
	StatusInterrupted = "interrupted"
)

// Source kinds.
const (
	KindREST = "rest"
	KindWXR  = "wxr"
)

// Phases a run goes through, in order, for the progress display.
var Phases = []string{"read", "settings", "schemas", "media", "authors", "terms", "content", "links", "menus", "redirects", "finish", "audit"}

// Actor is who started or stopped a job.
type Actor struct {
	ID    uuid.UUID
	Email string
}

// Source is what the owner typed on the first step.
type Source struct {
	Kind string // KindREST or KindWXR
	URL  string
	User string
	// Password is the application password. It is sealed and never returned.
	Password string
	// WXR is the uploaded export; it is copied to disk.
	WXR     io.Reader
	WXRName string
}

// Options are the choices of the options step.
type Options struct {
	// Types are the post types to import; nil imports all of them.
	Types  []string `json:"types"`
	Drafts bool     `json:"drafts"`
	Media  bool     `json:"media"`
	Menus  bool     `json:"menus"`
	// KeepPatterns keeps this site's URL patterns instead of copying
	// WordPress's permalink structure (old URLs then redirect).
	KeepPatterns bool `json:"keep_patterns"`
	// KeepEdited leaves items alone that were edited here since the last import.
	KeepEdited bool `json:"keep_edited"`
}

// DefaultOptions imports everything and copies the permalink structure.
func DefaultOptions() Options {
	return Options{Drafts: true, Media: true, Menus: true}
}

// ValidationError is a problem with something the owner typed.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Message }

// ErrNotFound is returned for an unknown run.
var ErrNotFound = errors.New("import run not found")

// ErrBusy is returned when the run already has a job.
var ErrBusy = errors.New("this import is already running")

// ErrImportRunning is returned when another import of this site holds the
// import lock: the command line, or a job of another run.
var ErrImportRunning = wpimport.ErrImportRunning

// ErrState is returned when the run is in a state the action does not fit.
var ErrState = errors.New("this action does not fit the import's state")

// Config wires a Manager.
type Config struct {
	Pool *pgxpool.Pool
	Deps *wpimport.Deps
	// Key seals stored credentials (see DeriveKey).
	Key []byte
	// SpoolDir keeps uploaded export files until the import completes.
	// Default: a directory under the system temp dir.
	SpoolDir string
	// Handler returns the site's own handler, which the URL audit asks for
	// every old URL; nil skips the audit.
	Handler func() http.Handler
	// Purge clears the server's caches (rendered pages, sitemaps, URL
	// patterns) after an import.
	Purge  func(ctx context.Context)
	Logger *slog.Logger
	// NoSweepLoop turns off the background retention sweep (tests call Sweep
	// themselves; a concurrent sweep would race them).
	NoSweepLoop bool
}

// Manager runs and tracks import jobs.
type Manager struct {
	cfg Config
	// AllowPrivate lets the source be on a private or loopback address. Only
	// tests set it; the server never does and nothing in a request can.
	AllowPrivate bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	jobs  map[uuid.UUID]*job
	cache *cached

	// runFn is the import itself; tests replace it with a fake engine.
	runFn func(ctx context.Context, id uuid.UUID, rec *record, o Options, actor Actor, host string, j *job)
	// gap, when set, runs after a start has changed the run's status and
	// before its job launches (tests use it to cancel in that window).
	gap func()
}

type job struct {
	m      *Manager
	ctx    context.Context
	cancel context.CancelFunc
	// release frees the site's import lock; nil when this job holds none.
	release func()
	once    sync.Once
	mu      sync.Mutex
	lines   []string
	// canceled is set when the owner asked to stop (a shutdown is not that).
	canceled bool
	// scan is a job that reads the source (its row says "scanning").
	scan bool
}

// cached keeps the last scanned source in memory so starting the import does
// not read the whole site a second time.
type cached struct {
	id     uuid.UUID
	dump   *wpimport.Dump
	client *wpimport.Client
	at     time.Time
}

const cacheTTL = 30 * time.Minute

// New creates a Manager. Close it on shutdown.
func New(cfg Config) *Manager {
	if cfg.SpoolDir == "" {
		cfg.SpoolDir = filepath.Join(os.TempDir(), "hatamoto-import")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, ctx: ctx, cancel: cancel, jobs: map[uuid.UUID]*job{}}
	m.runFn = m.runJob
	if cfg.Pool != nil && !cfg.NoSweepLoop {
		m.wg.Add(1)
		go m.sweepLoop()
	}
	return m
}

// Close stops every job and waits for it. A stopped run stays resumable.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
	if m.cfg.Deps != nil {
		m.cfg.Deps.Close()
	}
}

func (j *job) say(phase, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, time.Now().UTC().Format("15:04:05")+" "+phase+": "+msg)
	if len(j.lines) > 200 {
		j.lines = append([]string(nil), j.lines[len(j.lines)-200:]...)
	}
}

func (j *job) tail() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.lines...)
}

// stored is the run's options column: what the wizard needs to resume, never a secret.
// The CLI writes the same url and user keys.
type stored struct {
	Kind      string   `json:"kind"`
	URL       string   `json:"url"`
	User      string   `json:"user"`
	File      string   `json:"file,omitempty"`
	StartedBy string   `json:"started_by,omitempty"`
	Options   *Options `json:"wizard,omitempty"`
}

// ---- Begin: a source becomes a scanning run ----

// Begin records a run for the source and scans it in the background. The
// source's password is sealed; the upload is spooled to disk.
func (m *Manager) Begin(ctx context.Context, actor Actor, src Source) (uuid.UUID, error) {
	st := stored{Kind: src.Kind, StartedBy: actor.Email}
	ref := "upload"
	switch src.Kind {
	case KindREST:
		u, err := cleanSiteURL(src.URL)
		if err != nil {
			return uuid.Nil, err
		}
		user := strings.TrimSpace(src.User)
		if (user == "") != (src.Password == "") {
			field := "password"
			if user == "" {
				field = "user"
			}
			return uuid.Nil, &ValidationError{field, "Enter both the username and the application password, or leave both empty to read only what the site shows publicly."}
		}
		st.URL, st.User, ref = u, user, strings.ToLower(u)
	case KindWXR:
		if src.WXR == nil {
			return uuid.Nil, &ValidationError{"wxr", "Choose the export file."}
		}
		st.File = filepath.Base(src.WXRName)
	default:
		return uuid.Nil, &ValidationError{"kind", "Choose where to import from."}
	}
	opts, _ := json.Marshal(st)
	var id uuid.UUID
	if err := m.cfg.Pool.QueryRow(ctx, `INSERT INTO hatamoto_import_runs (source, source_ref, status, options) VALUES ($1, $2, $3, $4) RETURNING id`,
		wpimport.SourceName, wpimport.SourceName+":"+ref, StatusScanning, opts).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if src.Kind == KindREST {
		// A new scan of the site replaces the credentials of its older runs.
		_, _ = m.cfg.Pool.Exec(ctx, `DELETE FROM hatamoto_import_secrets s USING hatamoto_import_runs r
			WHERE r.id = s.run_id AND r.source_ref = $1 AND r.id <> $2 AND r.status NOT IN ('running', 'scanning')`, wpimport.SourceName+":"+ref, id)
	}
	if src.Kind == KindWXR {
		if err := m.spool(id, src.WXR); err != nil {
			m.deleteRun(ctx, id)
			return uuid.Nil, err
		}
	} else if src.Password != "" {
		if err := m.putSecret(ctx, id, src.Password); err != nil {
			m.deleteRun(ctx, id)
			return uuid.Nil, err
		}
	}
	m.audit(ctx, actor, "import.scan", id, map[string]any{"kind": st.Kind, "url": st.URL, "user": st.User, "file": st.File})
	j, err := m.reserve(id, true)
	if err != nil {
		return uuid.Nil, err
	}
	m.launch(id, j, func(ctx context.Context, j *job) { m.scanJob(ctx, id, j) })
	return id, nil
}

// cleanSiteURL validates the address of the WordPress site.
func cleanSiteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", &ValidationError{"url", "Enter the address of the WordPress site."}
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", &ValidationError{"url", "Enter a web address such as https://old-site.example.com."}
	}
	if u.User != nil {
		return "", &ValidationError{"url", "Leave the username and password out of the address: they have their own fields."}
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func (m *Manager) deleteRun(ctx context.Context, id uuid.UUID) {
	_, _ = m.cfg.Pool.Exec(ctx, `DELETE FROM hatamoto_import_runs WHERE id = $1`, id)
	_ = os.Remove(m.spoolPath(id))
}

// ---- spool and secrets ----

func (m *Manager) spoolPath(id uuid.UUID) string {
	return filepath.Join(m.cfg.SpoolDir, id.String()+".wxr")
}

// spool copies the upload to disk, refusing more than MaxWXRBytes.
func (m *Manager) spool(id uuid.UUID, r io.Reader) error {
	if err := checkSpoolDir(m.cfg.SpoolDir); err != nil {
		return err
	}
	f, err := os.OpenFile(m.spoolPath(id), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxWXRBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxWXRBytes {
		err = &ValidationError{"wxr", fmt.Sprintf("The file is larger than %d MB. Export fewer content types, or import from the site's address instead.", MaxWXRBytes>>20)}
	}
	if err == nil && n == 0 {
		err = &ValidationError{"wxr", "The file is empty."}
	}
	if err != nil {
		_ = os.Remove(m.spoolPath(id))
	}
	return err
}

type secret struct {
	Password string `json:"password"`
}

func (m *Manager) putSecret(ctx context.Context, id uuid.UUID, password string) error {
	plain, _ := json.Marshal(secret{Password: password})
	sealed, err := seal(m.cfg.Key, id, plain)
	if err != nil {
		return err
	}
	_, err = m.cfg.Pool.Exec(ctx, `INSERT INTO hatamoto_import_secrets (run_id, sealed) VALUES ($1, $2)
		ON CONFLICT (run_id) DO UPDATE SET sealed = EXCLUDED.sealed, created_at = now()`, id, sealed)
	return err
}

func (m *Manager) getSecret(ctx context.Context, id uuid.UUID) (string, bool, error) {
	var sealed []byte
	err := m.cfg.Pool.QueryRow(ctx, `SELECT sealed FROM hatamoto_import_secrets WHERE run_id = $1`, id).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := open(m.cfg.Key, id, sealed)
	if err != nil {
		return "", false, err
	}
	var s secret
	if err := json.Unmarshal(plain, &s); err != nil {
		return "", false, err
	}
	return s.Password, true, nil
}

// dropCredentials forgets the sealed password and the spooled file.
func (m *Manager) dropCredentials(ctx context.Context, id uuid.UUID) {
	_, _ = m.cfg.Pool.Exec(ctx, `DELETE FROM hatamoto_import_secrets WHERE run_id = $1`, id)
	_ = os.Remove(m.spoolPath(id))
}

// ---- jobs ----

// reserve takes the run's job slot. It is the first step of every start, so
// two requests for one run cannot both go on: the second gets ErrBusy.
func (m *Manager) reserve(id uuid.UUID, scan bool) (*job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return nil, ErrState // shutting down
	}
	if m.jobs[id] != nil {
		return nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(m.ctx)
	j := &job{m: m, ctx: ctx, cancel: cancel, scan: scan}
	m.jobs[id] = j
	m.wg.Add(1)
	return j, nil
}

// finish gives the slot and the import lock back. It removes only this job's
// own registry entry, and is safe to call twice.
func (j *job) finish(id uuid.UUID) {
	j.once.Do(func() {
		j.cancel()
		if j.release != nil {
			j.release()
		}
		j.m.mu.Lock()
		if j.m.jobs[id] == j {
			delete(j.m.jobs, id)
		}
		j.m.mu.Unlock()
		j.m.wg.Done()
	})
}

// launch runs fn as the reserved job j. A job canceled before it began (a stop
// that came in the start gap) never runs.
func (m *Manager) launch(id uuid.UUID, j *job, fn func(context.Context, *job)) {
	go func() {
		defer j.finish(id)
		defer func() {
			if p := recover(); p != nil {
				m.cfg.Logger.Error("import job panicked", "run", id, "panic", p)
				m.finishFailed(context.WithoutCancel(j.ctx), id, fmt.Errorf("the import stopped unexpectedly: %v", p))
			}
		}()
		if err := j.ctx.Err(); err != nil {
			m.settle(id, j, err)
			return
		}
		fn(j.ctx, j)
	}()
}

func (m *Manager) job(id uuid.UUID) *job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (j *job) wasCanceled() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.canceled
}

// settle records how a job that did not finish ended, and says which it was:
// "canceled" (the owner stopped it), "shutdown" (the server closed: the run
// stays running or scanning, which shows as interrupted and resumes) or "failed".
func (m *Manager) settle(id uuid.UUID, j *job, err error) string {
	bg := context.Background()
	switch {
	case j.wasCanceled():
		_, _ = m.cfg.Pool.Exec(bg, `UPDATE hatamoto_import_runs SET status = 'canceled', error = 'Stopped by the owner.', finished_at = now() WHERE id = $1`, id)
		return "canceled"
	case m.ctx.Err() != nil:
		status := StatusRunning
		if j.scan {
			status = StatusScanning
		}
		_, _ = m.cfg.Pool.Exec(bg, `UPDATE hatamoto_import_runs SET status = $2, error = '', finished_at = NULL WHERE id = $1`, id, status)
		return "shutdown"
	}
	m.finishFailed(bg, id, err)
	return "failed"
}

// scrub removes the run's password from an error message.
func (m *Manager) scrub(id uuid.UUID, err error) string {
	msg := err.Error()
	if pw, ok, _ := m.getSecret(context.Background(), id); ok && pw != "" {
		msg = strings.ReplaceAll(msg, pw, "***")
		msg = strings.ReplaceAll(msg, strings.ReplaceAll(pw, " ", ""), "***")
	}
	return msg
}

func (m *Manager) finishFailed(ctx context.Context, id uuid.UUID, err error) {
	_, _ = m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`, id, m.scrub(id, err))
}

// load reads the run's source.
func (m *Manager) load(ctx context.Context, id uuid.UUID, st stored, j *job) (*wpimport.Dump, *wpimport.Client, error) {
	j.say("read", "reading the source")
	if st.Kind == KindWXR {
		f, err := os.Open(m.spoolPath(id))
		if err != nil {
			return nil, nil, errors.New("the uploaded export file is no longer on the server: upload it again")
		}
		defer f.Close()
		d, err := wpimport.ParseWXR(f, wpimport.WXROptions{})
		return d, nil, err
	}
	pw, _, err := m.getSecret(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if st.User != "" && pw == "" {
		return nil, nil, errors.New("the application password is not stored: enter it again")
	}
	src, err := wpimport.NewRESTSource(wpimport.RESTOptions{
		ClientOptions: wpimport.ClientOptions{URL: st.URL, User: st.User, AppPassword: pw, AllowPrivate: m.AllowPrivate},
		Progress:      func(msg string) { j.say("read", msg) },
	})
	if err != nil {
		return nil, nil, err
	}
	d, err := src.Load(ctx)
	if err != nil {
		return nil, nil, err
	}
	return d, src.Client(), nil
}

func (m *Manager) scanJob(ctx context.Context, id uuid.UUID, j *job) {
	rec, err := m.record(ctx, id)
	if err != nil {
		m.settle(id, j, err)
		return
	}
	_, _ = m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET phase = 'read' WHERE id = $1`, id)
	dump, client, err := m.load(ctx, id, rec.st, j)
	if err == nil {
		var state *wpimport.SiteState
		store := wpimport.NewStore(m.cfg.Pool, dump.Site.URL)
		report := wpimport.Scan(dump, dump.Site.URL)
		if state, err = wpimport.InspectSite(ctx, m.cfg.Pool, wpimport.KeyPrefix(dump.Site.URL)); err == nil {
			report.ApplySiteState(state)
			var raw []byte
			raw, _ = json.Marshal(report)
			_, err = m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = 'scanned', phase = 'scanned', source_ref = $2, report = $3, error = '', finished_at = now() WHERE id = $1`,
				id, store.Ref(), raw)
			if err == nil {
				m.mu.Lock()
				m.cache = &cached{id: id, dump: dump, client: client, at: time.Now()}
				m.mu.Unlock()
				j.say("scan", "scan finished")
				return
			}
		}
	}
	m.settle(id, j, err)
}

// ---- Start, Resume, Cancel ----

// Start imports the scanned source with the chosen options. host is the
// address the audit asks the site at (the admin's own Host).
func (m *Manager) Start(ctx context.Context, id uuid.UUID, actor Actor, host string, o Options) error {
	return m.startRun(ctx, id, actor, host, &o, nil)
}

// Resume continues a failed, canceled or interrupted run with the options it
// had. cred replaces the stored credential (nil keeps it).
func (m *Manager) Resume(ctx context.Context, id uuid.UUID, actor Actor, host string, cred *Source) error {
	return m.startRun(ctx, id, actor, host, nil, cred)
}

func (m *Manager) startRun(ctx context.Context, id uuid.UUID, actor Actor, host string, o *Options, cred *Source) (err error) {
	// The slot comes first: nothing below changes anything until this request
	// is the only one starting the run.
	j, err := m.reserve(id, false)
	if err != nil {
		return err
	}
	launched := false
	defer func() {
		if !launched {
			j.finish(id)
		}
	}()
	rec, err := m.record(ctx, id)
	if err != nil {
		return err
	}
	switch rec.effective(false) {
	case StatusScanned, StatusFailed, StatusCanceled, StatusInterrupted:
	default:
		return ErrState
	}
	// One import at a time on the site, whichever process or run it is.
	if j.release, err = wpimport.AcquireImportLock(ctx, m.cfg.Pool); err != nil {
		return err
	}
	if cred != nil {
		if cred.Password != "" {
			if err := m.putSecret(ctx, id, cred.Password); err != nil {
				return err
			}
		}
		if cred.WXR != nil {
			if err := m.spool(id, cred.WXR); err != nil {
				return err
			}
		}
	}
	if o == nil {
		o = rec.st.Options
	}
	if o == nil {
		d := DefaultOptions()
		o = &d
	}
	rec.st.Options, rec.st.StartedBy = o, actor.Email
	raw, _ := json.Marshal(rec.st)
	// Without a scan report there is nothing to import from yet: scan again.
	scanOnly := !rec.hasReport()
	status := StatusRunning
	if scanOnly {
		status = StatusScanning
	}
	j.scan = scanOnly
	// The change is conditional on the status this request saw.
	tag, err := m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = $2, options = $3, error = '', finished_at = NULL WHERE id = $1 AND status = $4`, id, status, raw, rec.status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrState
	}
	if m.gap != nil {
		m.gap()
	}
	action := "import.start"
	if cred != nil || rec.status == StatusFailed || rec.status == StatusCanceled || rec.status == StatusRunning {
		action = "import.resume"
	}
	m.audit(ctx, actor, action, id, map[string]any{"options": o, "url": rec.st.URL, "kind": rec.st.Kind})
	launched = true
	if scanOnly {
		m.launch(id, j, func(ctx context.Context, j *job) { m.scanJob(ctx, id, j) })
		return nil
	}
	m.launch(id, j, func(ctx context.Context, j *job) { m.runFn(ctx, id, rec, *o, actor, host, j) })
	return nil
}

func (m *Manager) runJob(ctx context.Context, id uuid.UUID, rec *record, o Options, actor Actor, host string, j *job) {
	fail := func(err error) {
		how := m.settle(id, j, err)
		m.audit(context.Background(), actor, "import.stopped", id, map[string]any{"error": m.scrub(id, err), "canceled": how == "canceled", "shutdown": how == "shutdown"})
	}
	_, _ = m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET phase = 'read' WHERE id = $1`, id)
	dump, client := m.takeCached(id)
	if dump == nil {
		var err error
		if dump, client, err = m.load(ctx, id, rec.st, j); err != nil {
			fail(err)
			return
		}
	}
	wpimport.Selection{Types: o.Types, Drafts: o.Drafts, Menus: o.Menus}.Apply(dump)
	store := wpimport.NewStore(m.cfg.Pool, dump.Site.URL)
	if store.Ref() != rec.sourceRef {
		fail(fmt.Errorf("the source now identifies as %s, not %s: start again from the first step", store.Ref(), rec.sourceRef))
		return
	}
	opts := wpimport.Options{
		Actor: &actor.ID, AllowPrivate: m.AllowPrivate, Client: client, NoPermalinks: o.KeepPatterns, KeepEdited: o.KeepEdited,
		SkipMedia: !o.Media, Progress: j.say, Purge: m.cfg.Purge, Stored: map[string]any{"kind": rec.st.Kind, "url": rec.st.URL, "user": rec.st.User, "wizard": o},
	}
	if m.cfg.Handler != nil {
		if h := m.cfg.Handler(); h != nil {
			opts.Audit = wpimport.HandlerProber{Handler: h, Host: host}
		}
	}
	runner, err := wpimport.NewRunner(m.cfg.Deps, store, dump, opts)
	if err != nil {
		fail(err)
		return
	}
	res, err := runner.Run(ctx, id)
	if err != nil {
		fail(err)
		return
	}
	m.dropCredentials(context.WithoutCancel(ctx), id)
	m.audit(context.WithoutCancel(ctx), actor, "import.completed", id, map[string]any{
		"created": res.Created, "updated": res.Updated, "failed": res.Failed, "redirects": res.Redirects, "media": res.MediaImported, "mode": res.SiteMode,
	})
}

func (m *Manager) takeCached(id uuid.UUID) (*wpimport.Dump, *wpimport.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cache
	m.cache = nil
	if c == nil || c.id != id || time.Since(c.at) > cacheTTL {
		return nil, nil
	}
	return c.dump, c.client
}

// Cancel stops a job. A run without a job in this process (after a restart)
// is marked canceled directly. A job that is still starting is stopped too:
// the stop wins and the job never runs.
func (m *Manager) Cancel(ctx context.Context, id uuid.UUID, actor Actor) error {
	if j := m.job(id); j != nil {
		j.mu.Lock()
		j.canceled = true
		j.mu.Unlock()
		j.cancel()
		m.audit(ctx, actor, "import.cancel", id, nil)
		return nil
	}
	// Hold the slot while the row changes, so a start cannot slip in between.
	j, err := m.reserve(id, false)
	if errors.Is(err, ErrBusy) {
		return m.Cancel(ctx, id, actor)
	}
	if err != nil {
		return err
	}
	defer j.finish(id)
	rec, err := m.record(ctx, id)
	if err != nil {
		return err
	}
	if rec.effective(false) != StatusInterrupted {
		return ErrState
	}
	tag, err := m.cfg.Pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = 'canceled', error = 'Stopped by the owner.', finished_at = now() WHERE id = $1 AND status = $2`, id, rec.status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrState
	}
	m.audit(ctx, actor, "import.cancel", id, nil)
	return nil
}

// ---- audit ----

// maxAuditError bounds the error text of an audit row: it can carry remote strings.
const maxAuditError = 1024

func (m *Manager) audit(ctx context.Context, actor Actor, action string, run uuid.UUID, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	if msg, ok := detail["error"].(string); ok && len(msg) > maxAuditError {
		detail["error"] = strings.ToValidUTF8(msg[:maxAuditError], "") + "…"
	}
	raw, _ := json.Marshal(detail)
	var actorID *uuid.UUID
	if actor.ID != uuid.Nil {
		actorID = &actor.ID
	}
	if _, err := m.cfg.Pool.Exec(ctx, `INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES ($1, $2, $3, $4, $5)`,
		actorID, actor.Email, action, "import_run:"+run.String(), raw); err != nil {
		m.cfg.Logger.Error("admin audit write failed", "action", action, "err", err)
	}
	m.cfg.Logger.Info("admin audit", "action", action, "actor", actor.Email, "run", run, "detail", string(raw))
}
