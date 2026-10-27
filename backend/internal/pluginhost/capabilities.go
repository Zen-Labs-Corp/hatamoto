package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/sdk"
)

// Scheduler runs a function every interval (worker.Scheduler). Scheduling an
// id again replaces the job; Unschedule stops every job whose id starts with
// prefix.
type Scheduler interface {
	Schedule(id string, every time.Duration, fn func(context.Context) error) error
	Unschedule(prefix string)
}

// Config wires the runtime's capabilities. Every field is optional: without
// Services the host functions answer "unavailable", without Grants nothing is
// ever granted (a memory store), without Scheduler cron jobs do not run.
type Config struct {
	Services  Services
	Grants    Grants
	Scheduler Scheduler
}

type capabilities struct {
	grants Grants
	api    *API
	cron   Scheduler
	admin  *RouteRegistry
	public *RouteRegistry
	db     *PluginDB
}

// Configure sets the capabilities. Call it before loading plugins.
func (r *Runtime) Configure(c Config) {
	if c.Grants == nil {
		c.Grants = NewGrantStore(nil)
	}
	r.caps = capabilities{grants: c.Grants, cron: c.Scheduler, db: c.Services.DB}
	r.caps.api = NewAPI(c.Services, c.Grants, r.manifest, r.log)
	r.host.SetHostCall(r.caps.api.Call)
	r.caps.admin = NewRouteRegistry(r.host.Call, false, r.log)
	r.caps.public = NewRouteRegistry(r.host.Call, true, r.log)
}

// Grants returns the grant store.
func (r *Runtime) Grants() Grants { return r.caps.grants }

// AdminRoutes and PublicRoutes are the two route registries the server mounts.
func (r *Runtime) AdminRoutes() *RouteRegistry  { return r.caps.admin }
func (r *Runtime) PublicRoutes() *RouteRegistry { return r.caps.public }

func (r *Runtime) manifest(name string) (sdk.Manifest, bool) {
	st, ok := r.status(name)
	return st.Manifest, ok && st.Loaded
}

// Assets are the files a plugin directory carries next to plugin.wasm.
type Assets struct {
	Migrations []Migration       // migrations/*.sql
	Partials   map[string]string // partials/<name>.html
	Sections   map[string]string // sections/<type>.html
}

const (
	maxAssetFiles = 64
	maxAssetBytes = 256 << 10
)

var assetName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func readAssets(pdir string) (Assets, error) { return AssetsFromFS(os.DirFS(pdir)) }

// AssetsFromFS reads migrations/, partials/ and sections/ of a plugin (a
// directory or a package). A bad asset fails the plugin's load: half a plugin
// is worse than none.
func AssetsFromFS(root fs.FS) (Assets, error) {
	var a Assets
	migs, err := ReadMigrations(root, "migrations")
	if err != nil {
		return a, err
	}
	a.Migrations = migs
	read := func(dir string) (map[string]string, error) {
		entries, err := fs.ReadDir(root, dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		out := map[string]string{}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".html")
			if e.IsDir() || !ok {
				continue
			}
			if !assetName.MatchString(name) {
				return nil, fmt.Errorf("%s/%s: name must match %s", dir, e.Name(), assetName)
			}
			if len(out) >= maxAssetFiles {
				return nil, fmt.Errorf("%s: more than %d files", dir, maxAssetFiles)
			}
			b, err := fs.ReadFile(root, filepath.ToSlash(filepath.Join(dir, e.Name())))
			if err != nil {
				return nil, err
			}
			if len(b) > maxAssetBytes {
				return nil, fmt.Errorf("%s/%s is larger than %d bytes", dir, e.Name(), maxAssetBytes)
			}
			out[name] = string(b)
		}
		return out, nil
	}
	if a.Partials, err = read("partials"); err != nil {
		return a, err
	}
	a.Sections, err = read("sections")
	return a, err
}

// activate makes a freshly loaded plugin's routes, cron jobs, template funcs,
// partials and sections live and prepares its data. It returns the reason the
// plugin's data is not ready ("" when it is, or none was asked for).
func (r *Runtime) activate(ctx context.Context, m sdk.Manifest, a Assets) string {
	r.caps.admin.Set(m.Name, m.Routes)
	r.caps.public.Set(m.Name, m.Routes)
	r.scheduleCron(m)
	r.registerTemplates(m, a)
	return r.prepareData(ctx, m, a)
}

func (r *Runtime) deactivate(name string) {
	r.caps.admin.Set(name, nil)
	r.caps.public.Set(name, nil)
	r.unscheduleCron(name)
	render.RemovePlugin(name)
	r.mu.Lock()
	delete(r.assets, name)
	r.mu.Unlock()
}

// prepareData provisions the plugin's schema when it holds kv or db and runs
// its migrations when it holds db.
func (r *Runtime) prepareData(ctx context.Context, m sdk.Manifest, a Assets) string {
	g := r.caps.grants
	if r.caps.db == nil || !(g.Has(m.Name, sdk.PermKV) || g.Has(m.Name, sdk.PermDB)) {
		return ""
	}
	if err := r.caps.db.Reprovision(ctx, m.Name); err != nil {
		r.log.Error("plugin data not provisioned", slog.String("plugin", m.Name), slog.Any("error", err))
		return err.Error()
	}
	if !g.Has(m.Name, sdk.PermDB) || len(a.Migrations) == 0 {
		return ""
	}
	n, err := r.caps.db.Migrate(ctx, m.Name, a.Migrations)
	if err != nil {
		r.log.Error("plugin migrations failed", slog.String("plugin", m.Name), slog.Any("error", err))
		return err.Error()
	}
	if n > 0 {
		r.log.Info("plugin migrations applied", slog.String("plugin", m.Name), slog.Int("count", n))
	}
	return ""
}

// ErrPluginNotFound is returned by Grant for a plugin that is not installed.
var ErrPluginNotFound = errors.New("plugin not found")

// ErrNotRequested is returned by Grant for a permission the manifest does not list.
var ErrNotRequested = errors.New("permission not requested by the plugin")

// ErrCommunityScripts is returned for a site.scripts grant to a community package.
var ErrCommunityScripts = errors.New("site.scripts cannot be granted to a community package")

// SetGrantPolicy installs a check Grant runs before it stores a grant; the
// server wires the package policy here (pluginhost cannot import packages).
func (r *Runtime) SetGrantPolicy(f func(plugin string, perms []sdk.Permission) error) {
	r.grantPolicy.Store(&f)
}

// Grant sets the capabilities the owner allows plugin: perms must all be
// listed in its manifest, and replace any earlier grant (grant fewer to
// revoke). Data the new grants need is prepared at once. It returns the
// plugin's resulting status.
func (r *Runtime) Grant(ctx context.Context, name string, perms []sdk.Permission) (Status, error) {
	st, ok := r.status(name)
	if !ok {
		return Status{}, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}
	for _, p := range perms {
		if !slices.Contains(st.Manifest.Permissions, p) {
			return st, fmt.Errorf("%w: %q", ErrNotRequested, p)
		}
	}
	if f := r.grantPolicy.Load(); f != nil {
		if err := (*f)(name, perms); err != nil {
			return st, err
		}
	}
	if err := r.caps.grants.Set(ctx, name, perms); err != nil {
		return st, err
	}
	if st.Loaded {
		r.mu.Lock()
		a := r.assets[name]
		r.mu.Unlock()
		st.DataError = r.prepareData(ctx, st.Manifest, a)
		if len(st.Manifest.BuilderSources) > 0 {
			// builder.data is registered only while granted: apply the change now.
			r.engine.RemovePlugin(name)
			st.Hooks = r.register(st.Manifest)
		}
		st = r.record(st)
	}
	r.syncExtSources()
	return st, nil
}

// --- cron ---------------------------------------------------------------------

func cronID(plugin, job string) string { return "plugin:" + plugin + ":" + job }

func (r *Runtime) scheduleCron(m sdk.Manifest) {
	r.unscheduleCron(m.Name)
	if r.caps.cron == nil {
		return
	}
	for _, c := range m.Cron {
		export := "on_cron_" + c.Name
		if !r.host.HasExport(m.Name, export) {
			r.log.Warn("plugin cron job has no export; not scheduled", slog.String("plugin", m.Name), slog.String("job", c.Name), slog.String("export", export))
			continue
		}
		every, _ := time.ParseDuration(c.Every) // validated by the manifest
		name, job := m.Name, c.Name
		err := r.caps.cron.Schedule(cronID(name, job), every, func(ctx context.Context) error {
			in, _ := json.Marshal(sdk.CronEvent{Name: job, ScheduledAt: time.Now().UTC().Format(time.RFC3339)})
			_, err := r.host.Call(ctx, name, export, in)
			return err
		})
		if err != nil {
			r.log.Error("plugin cron job not scheduled", slog.String("plugin", name), slog.String("job", job), slog.Any("error", err))
		}
	}
}

func (r *Runtime) unscheduleCron(name string) {
	if r.caps.cron != nil {
		r.caps.cron.Unschedule("plugin:" + name + ":")
	}
}

// --- template funcs, partials, sections ---------------------------------------------

const (
	maxFuncBytes = 64 << 10
	// funcPause is how long one template func answers "" at once after
	// funcFailLimit consecutive failures, so a hung func costs a render at
	// most funcFailLimit timeouts per pause instead of one per call.
	funcPause     = 30 * time.Second
	funcFailLimit = 3
)

// funcBreaker pauses template funcs that keep failing. It is keyed per func
// ("plugin/export"), like the hook breakers: one broken func does not blank
// the plugin's other funcs, and a single failure does not pause anything.
type funcBreaker struct {
	mu    sync.Mutex
	fails map[string]int
	until map[string]time.Time
}

func (b *funcBreaker) paused(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.until[key])
}

// fail counts one failure; the funcFailLimit-th consecutive one pauses the func.
func (b *funcBreaker) fail(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fails == nil {
		b.fails = map[string]int{}
		b.until = map[string]time.Time{}
	}
	b.fails[key]++
	if b.fails[key] >= funcFailLimit {
		b.fails[key] = 0
		b.until[key] = time.Now().Add(funcPause)
	}
}

// ok ends a failure streak.
func (b *funcBreaker) ok(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fails, key)
}

// countsAsFuncFailure: a busy plugin (ErrSaturated) or an open hook breaker is
// not a new strike against the func.
func countsAsFuncFailure(err error) bool {
	return !errors.Is(err, wasmhost.ErrSaturated) && !errors.Is(err, wasmhost.ErrCircuitOpen)
}

// templateFunc is the template func plugin_<plugin>_<fn>. Its output is plain
// text (html/template escapes it). A failure, a timeout or an oversized answer
// renders as an empty string and is logged, and never turns the page into a
// 500. After funcFailLimit consecutive failures of one func it is skipped for
// funcPause; saturation (wasmhost.ErrSaturated: the plugin is only busy) and
// breaker rejections do not count toward that. Every blank, and every call
// skipped during the pause, marks the render degraded (render.NoteDegraded) so
// the page is not cached.
func (r *Runtime) templateFunc(plugin, fn, export string) func(args ...any) (string, error) {
	return func(args ...any) (string, error) {
		key := plugin + "/" + export
		if r.funcs.paused(key) {
			render.NoteDegraded() // the page lacks this output: keep it out of the cache
			return "", nil
		}
		fail := func(err error) (string, error) {
			if countsAsFuncFailure(err) {
				r.funcs.fail(key)
			}
			render.NoteDegraded()
			r.log.Warn("plugin template func failed; output left empty", slog.String("plugin", plugin), slog.String("func", fn), slog.Any("error", err))
			return "", nil
		}
		in, err := json.Marshal(args)
		if err != nil {
			return fail(err)
		}
		// No deadline of our own: wasmhost bounds the call (slot wait, cold
		// start, then the func's own hook timeout: 250 ms unless the manifest
		// sets hook_timeouts_ms["func_<name>"]), and the wait for an instance
		// must not eat the guest's time.
		out, err := r.host.Call(context.Background(), plugin, export, in)
		if err != nil {
			return fail(err)
		}
		if len(out) > maxFuncBytes {
			return fail(fmt.Errorf("output over %d bytes", maxFuncBytes))
		}
		r.funcs.ok(key)
		return string(out), nil
	}
}

func (r *Runtime) registerTemplates(m sdk.Manifest, a Assets) {
	render.RemovePlugin(m.Name)
	for _, fn := range m.Funcs {
		export := "func_" + fn
		if !r.host.HasExport(m.Name, export) {
			r.log.Warn("plugin template func has no export", slog.String("plugin", m.Name), slog.String("func", fn))
			continue
		}
		err := render.RegisterPluginFunc(m.Name, fn, r.templateFunc(m.Name, fn, export))
		if err != nil {
			r.log.Error("plugin template func not registered", slog.String("plugin", m.Name), slog.Any("error", err))
		}
	}
	for name, src := range a.Partials {
		if err := render.RegisterPluginPartial(m.Name, name, src); err != nil {
			r.log.Error("plugin partial not registered", slog.String("plugin", m.Name), slog.Any("error", err))
		}
	}
	for typ, src := range a.Sections {
		if err := render.RegisterPluginSection(m.Name, typ, src); err != nil {
			r.log.Error("plugin section not registered", slog.String("plugin", m.Name), slog.Any("error", err))
		}
	}
}

// CallHost runs one host function on behalf of plugin, with the plugin's
// permission check, and returns the sdk.CallResponse JSON. It is the path the
// admin's plugin-UI bridge uses.
func (r *Runtime) CallHost(ctx context.Context, plugin string, req sdk.CallRequest) []byte {
	in, _ := json.Marshal(req)
	return r.caps.api.Call(ctx, plugin, in)
}
