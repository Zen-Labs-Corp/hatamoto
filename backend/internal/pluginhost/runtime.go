// Package pluginhost connects the WASM plugin runtime (internal/wasmhost) to
// the rest of the core: it loads plugins from a directory, maps their exports
// onto the hooks engine, and keeps the settings store and the adapters that
// turn core events into SDK hook payloads.
package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/sdk"
)

// Plugin file names inside <plugin dir>/<name>/.
const (
	ManifestFile = "plugin.json"
	WasmFile     = "plugin.wasm"
)

// maxWasmBytes bounds a plugin module read from disk.
const maxWasmBytes = 64 << 20

// Status is the outcome of loading one plugin; it is what GET /api/v1/plugins
// reports.
type Status struct {
	Name     string
	Manifest sdk.Manifest
	Loaded   bool
	Error    string   // why it did not load
	Hooks    []string // hook names the module exports a handler for
	// DataError says why the plugin's own data (schema, migrations) is not
	// ready; the plugin itself is loaded.
	DataError string
}

// Runtime loads plugins into a wasmhost.Host and registers their hook
// handlers on the engine.
type Runtime struct {
	host   *wasmhost.Host
	engine *hooks.Engine
	log    *slog.Logger

	caps   capabilities
	funcs  funcBreaker
	assets map[string]Assets // by plugin, kept for re-running migrations on grant

	// scriptsDropped holds plugins whose render.head script-src tags were dropped
	// for want of site.scripts.
	scriptsDropped sync.Map

	// grantPolicy vetoes a grant before it is stored (see SetGrantPolicy).
	// scriptsDeny refuses a plugin's scripts whatever is granted (see SetScriptsDeny).
	scriptsDeny atomic.Pointer[func(plugin string) bool]
	grantPolicy atomic.Pointer[func(plugin string, perms []sdk.Permission) error]

	mu       sync.Mutex
	statuses []Status
}

// NewRuntime returns a Runtime. cacheDir is the compiled-code cache ("" =
// memory only).
func NewRuntime(log *slog.Logger, engine *hooks.Engine, cacheDir string) (*Runtime, error) {
	if log == nil {
		log = slog.Default()
	}
	host, err := wasmhost.New(log, cacheDir)
	if err != nil {
		return nil, err
	}
	r := &Runtime{host: host, engine: engine, log: log, assets: map[string]Assets{}}
	r.Configure(Config{})
	return r, nil
}

// Close unloads every plugin.
func (r *Runtime) Close(ctx context.Context) {
	r.host.Close(ctx)
	if r.caps.db != nil {
		r.caps.db.Close()
	}
}

// Host exposes the underlying runtime (tests).
func (r *Runtime) Host() *wasmhost.Host { return r.host }

// Statuses returns the load results so far: loaded plugins and failures.
func (r *Runtime) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.statuses)
}

// LoadDir loads every <dir>/<name>/ that holds a plugin.json, in name order.
// A plugin that fails to load is recorded and logged; the others and the
// server are unaffected. A missing dir means no plugins.
func (r *Runtime) LoadDir(ctx context.Context, dir string) []Status {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.log.Error("plugins: read plugin dir", slog.String("dir", dir), slog.Any("error", err))
		}
		return nil
	}
	var out []Status
	for _, e := range entries {
		pdir := filepath.Join(dir, e.Name())
		if !isPluginDir(dir, e) {
			continue
		}
		if _, err := os.Stat(filepath.Join(pdir, ManifestFile)); err != nil {
			continue
		}
		out = append(out, r.loadFrom(ctx, e.Name(), pdir))
	}
	return out
}

// isPluginDir reports whether e is a visible directory, following symlinks
// (a plugin under development is often linked into the plugin dir).
func isPluginDir(dir string, e os.DirEntry) bool {
	if strings.HasPrefix(e.Name(), ".") {
		return false
	}
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && fi.IsDir()
}

// ListDir reads the manifests under dir without compiling anything. A plugin
// whose manifest cannot be read is listed with its Error. A missing dir is
// no plugins; any other failure to read dir is returned.
func ListDir(dir string) ([]Status, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []Status
	for _, e := range entries {
		if !isPluginDir(dir, e) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), ManifestFile))
		if err != nil {
			continue
		}
		st := Status{Name: e.Name()}
		if err := json.Unmarshal(raw, &st.Manifest); err != nil {
			st.Error = err.Error()
		} else if err := st.Manifest.Validate(); err != nil {
			st.Error = err.Error()
		}
		out = append(out, st)
	}
	return out, nil
}

func (r *Runtime) loadFrom(ctx context.Context, dirName, pdir string) Status {
	m, wasm, err := readPlugin(pdir)
	if err == nil && m.Name != dirName {
		err = fmt.Errorf("plugin.json name %q does not match directory %q", m.Name, dirName)
	}
	if err != nil {
		return r.record(Status{Name: dirName, Manifest: m, Error: err.Error()})
	}
	assets, err := readAssets(pdir)
	if err != nil {
		return r.record(Status{Name: dirName, Manifest: m, Error: err.Error()})
	}
	return r.LoadWith(ctx, m, wasm, assets)
}

func readPlugin(pdir string) (sdk.Manifest, []byte, error) {
	var m sdk.Manifest
	raw, err := os.ReadFile(filepath.Join(pdir, ManifestFile))
	if err != nil {
		return m, nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	f, err := os.Open(filepath.Join(pdir, WasmFile))
	if err != nil {
		return m, nil, err
	}
	defer f.Close()
	// Read through the handle that was opened, bounded: a FIFO or /dev/zero
	// behind the path cannot grow the read.
	wasm, err := io.ReadAll(io.LimitReader(f, maxWasmBytes+1))
	if err == nil && len(wasm) > maxWasmBytes {
		err = fmt.Errorf("%s is larger than %d bytes", WasmFile, maxWasmBytes)
	}
	return m, wasm, err
}

// Load is LoadWith without assets.
func (r *Runtime) Load(ctx context.Context, m sdk.Manifest, wasm []byte) Status {
	return r.LoadWith(ctx, m, wasm, Assets{})
}

// LoadWith validates, compiles and registers one plugin with its assets
// (migrations, partials, sections). Never returns a Go error: the result,
// including a failure, is the Status.
func (r *Runtime) LoadWith(ctx context.Context, m sdk.Manifest, wasm []byte, assets Assets) Status {
	st := Status{Name: m.Name, Manifest: m}
	fail := func(err error) Status {
		st.Error = err.Error()
		r.log.Error("plugin not loaded", slog.String("plugin", m.Name), slog.String("error", st.Error))
		// A failed re-load leaves the previous module and its hooks running,
		// so keep reporting it as loaded, with the reason the update failed.
		if prev, ok := r.status(m.Name); ok && prev.Loaded {
			prev.Error = "reload failed: " + st.Error
			return r.record(prev)
		}
		return r.record(st)
	}
	if err := m.Validate(); err != nil {
		return fail(err)
	}
	if err := SDKRefusal(m); err != nil {
		return fail(err)
	}
	if len(m.SettingsSchema) > 0 {
		if _, err := CompileSchema(m.SettingsSchema); err != nil {
			return fail(err)
		}
	}
	if err := r.host.Load(ctx, m, wasm); err != nil {
		return fail(err)
	}
	r.engine.RemovePlugin(m.Name) // a re-load replaced the module: drop the old module's hooks
	st.Loaded = true
	r.record(st) // host functions look the manifest up here: before any hook can run
	st.Hooks = r.register(m)
	r.mu.Lock()
	r.assets[m.Name] = assets
	r.mu.Unlock()
	// The manifest must be visible to host functions before routes, cron and
	// template funcs go live and can call one.
	r.record(st)
	st.DataError = r.activate(ctx, m, assets)
	r.syncExtSources()
	r.log.Info("plugin loaded", slog.String("plugin", m.Name), slog.String("version", m.Version), slog.Any("hooks", st.Hooks))
	return r.record(st)
}

// Unload removes the plugin's hooks and frees its module.
func (r *Runtime) Unload(ctx context.Context, name string) {
	r.deactivate(name)
	r.scriptsDropped.Delete(name) // the note is about the version that is gone
	r.engine.RemovePlugin(name)
	r.host.Unload(ctx, name)
	r.mu.Lock()
	r.statuses = slices.DeleteFunc(r.statuses, func(s Status) bool { return s.Name == name })
	r.mu.Unlock()
	r.syncExtSources()
}

func (r *Runtime) status(name string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.statuses {
		if s.Name == name {
			return s, true
		}
	}
	return Status{}, false
}

func (r *Runtime) record(st Status) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = slices.DeleteFunc(r.statuses, func(s Status) bool { return s.Name == st.Name })
	r.statuses = append(r.statuses, st)
	return st
}

// register adds a handler to the engine for every hook the module exports and
// returns those hook names.
func (r *Runtime) register(m sdk.Manifest) []string {
	name := m.Name
	reg := r.engine.For(name)
	var got []string
	for _, hook := range sdk.ActionNames {
		export := sdk.ExportName("on_", hook)
		if r.host.HasExport(name, export) {
			reg.AddAction(hook, 0, r.action(m, hook, export))
			got = append(got, hook)
		}
	}
	for _, hook := range sdk.FilterNames {
		export := sdk.ExportName("filter_", hook)
		if hook == sdk.BuilderData.Name && !r.offersBuilderData(m) {
			continue // not granted or no declared sources: the filter is not registered
		}
		if r.host.HasExport(name, export) {
			reg.AddFilter(hook, 0, r.hookFilter(m, hook, export))
			got = append(got, hook)
		}
	}
	return got
}

// offersBuilderData reports whether the plugin answers builder.data: it
// declares sources and the owner granted the permission.
func (r *Runtime) offersBuilderData(m sdk.Manifest) bool {
	return len(m.BuilderSources) > 0 && r.caps.grants.Has(m.Name, sdk.PermBuilderData)
}

// action returns a hooks action that calls the plugin's on_<hook> export. A
// failure is logged and goes no further.
func (r *Runtime) action(m sdk.Manifest, hook, export string) func(context.Context, any) {
	name := m.Name
	return func(ctx context.Context, payload any) {
		if hook == sdk.FormSubmitted.Name && !r.caps.grants.Has(name, sdk.PermFormsRead) {
			return // the payload carries submitted data: only plugins granted forms.read see it
		}
		in, err := json.Marshal(payload)
		if err == nil {
			_, err = r.host.Call(ctx, name, export, in)
		}
		if err != nil {
			r.log.Warn("plugin action failed",
				slog.String("plugin", name), slog.String("hook", hook), slog.Any("error", err))
		}
	}
}

// filter returns a hooks filter that calls the plugin's filter_<hook> export.
// Any failure comes back as an error, which the engine logs and answers by
// keeping the value unchanged (fail open); only a deliberate rejection
// becomes *sdk.Reject.
func (r *Runtime) filter(name, export string) func(context.Context, any) (any, error) {
	return func(ctx context.Context, v any) (any, error) {
		in, err := json.Marshal(v)
		if err != nil {
			return v, err
		}
		out, err := r.host.Call(ctx, name, export, in)
		if err != nil {
			if reason, ok := rejection(err); ok {
				return v, &sdk.Reject{Reason: reason}
			}
			return v, err
		}
		if len(out) == 0 {
			return v, nil // no output: the plugin left the value as it is
		}
		res, err := decodeOutput(v, in, out)
		if err != nil {
			return v, fmt.Errorf("decode %s output: %w", export, err)
		}
		return res, nil
	}
}

func rejection(err error) (string, bool) {
	reason, ok := strings.CutPrefix(err.Error(), sdk.RejectPrefix)
	return strings.TrimSpace(reason), ok
}
