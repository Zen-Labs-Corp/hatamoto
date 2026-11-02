// Package wasmhost runs Hatamoto plugins: Extism (wazero) modules built from
// standard Go (or any Extism PDK language) with a hard sandbox.
//
// Every plugin gets, from its manifest: a cap on linear memory, a per-hook
// call timeout (always on: without it neither the timeout nor a cancelled
// request can stop a spinning guest; 250 ms for filters and 10 s for actions
// unless the manifest says otherwise; it starts once an instance is ready,
// waiting for a slot and starting a cold instance are bounded separately), an empty filesystem, no network, and a pool of
// instances with an admission cap. An instance that saw any error is thrown
// away, because a Go runtime that trapped is poisoned. Plugins load and
// unload without restarting; the registry is an immutable map behind an
// atomic pointer, so the request path takes no host-wide lock.
package wasmhost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"

	"hatamoto.dev/sdk"
)

// Per-call limits that are not plugin-tunable.
const (
	maxHTTPResponseBytes = 0        // no network: Extism http_request is never granted (see http.fetch)
	maxVarBytes          = 64 << 10 // Extism var store

	maxLogBytes = 4 << 10 // one guest log line, truncated beyond this
	logBurst    = 20      // guest log lines allowed at once ...
	logPerSec   = 10      // ... refilled at this rate; the rest are dropped

	// A hook that traps or times out this many calls in a row is skipped for
	// breakerCooldown (the circuit breaker, per plugin and export).
	breakerThreshold = 3
	breakerCooldown  = 30 * time.Second

	// Getting ready for a call is bounded apart from the guest's deadline
	// (see Call): a call waits at most slotWait(export, timeout) for a free instance
	// slot and maxStartWait for a cold instance to start.
	minSlotWait  = 2 * time.Second
	maxStartWait = 5 * time.Second
)

// slotWait is how long a call to export with the given hook timeout may wait
// for a free slot. Request-path exports (filters and template funcs) wait
// minSlotWait at most, however long their own timeout is: a page that calls
// several of them under load must not queue for each one's full timeout.
// Actions, routes and cron jobs wait as long as their own timeout if that is
// longer.
func slotWait(export string, timeout time.Duration) time.Duration {
	if strings.HasPrefix(export, "filter_") || strings.HasPrefix(export, "func_") {
		return minSlotWait
	}
	return max(minSlotWait, timeout)
}

// ErrNotLoaded is returned when calling a plugin that is not (or no longer) loaded.
var ErrNotLoaded = errors.New("wasmhost: plugin not loaded")

// ErrSaturated wraps the context error of a call that gave up waiting for a
// free instance slot.
var ErrSaturated = errors.New("wasmhost: plugin saturated")

// ErrOutputTooLarge is returned when a call's output exceeds the plugin's
// max_output_bytes.
var ErrOutputTooLarge = errors.New("wasmhost: plugin output too large")

// ErrCircuitOpen is returned without running the guest while the circuit
// breaker for that plugin export is open.
var ErrCircuitOpen = errors.New("wasmhost: plugin hook disabled after repeated failures")

// Host owns every loaded plugin.
type Host struct {
	log   *slog.Logger
	cache wazero.CompilationCache
	reg   atomic.Pointer[map[string]*plugin]
	mu    sync.Mutex // serialises Load/Unload writers only

	hostCall HostCall // set before the first Load
}

// HostCall serves a plugin's hm_call import: in is the guest's request, the
// result goes back to it. ctx is the context of the hook call that is running
// the guest (its values and deadline), so the callee can bind itself to the
// call. The callee never fails: errors are encoded in the response.
type HostCall func(ctx context.Context, plugin string, in []byte) []byte

// New returns a Host. cacheDir != "" persists wazero's compiled code there so
// a restart does not recompile every module (about a second per Go guest);
// otherwise compiled code is cached in memory only. The directory is created
// private (0700): it holds native machine code the server executes. The cache
// is rebuildable, so a directory that cannot be used costs speed, not
// correctness: it is logged and the cache stays in memory.
func New(log *slog.Logger, cacheDir string) (*Host, error) {
	if log == nil {
		log = slog.Default()
	}
	h := &Host{log: log}
	h.cache = wazero.NewCompilationCache()
	if cacheDir != "" {
		if c, err := openCache(cacheDir); err != nil {
			log.Warn("wasmhost: compilation cache directory unusable; compiled code stays in memory",
				slog.String("dir", cacheDir), slog.Any("error", err))
		} else {
			h.cache = c
		}
	}
	empty := map[string]*plugin{}
	h.reg.Store(&empty)
	extism.SetLogLevel(extism.LogLevelInfo)
	return h, nil
}

func openCache(dir string) (wazero.CompilationCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return wazero.NewCompilationCacheWithDir(dir)
}

type plugin struct {
	manifest sdk.Manifest
	compiled *extism.CompiledPlugin
	pool     chan *extism.Plugin
	slots    chan struct{} // one token per concurrent call; bounds live instances
	log      *slog.Logger

	mu     sync.RWMutex // RLock per call; Lock to close
	closed bool

	discarded atomic.Int64

	brkMu    sync.Mutex
	breakers map[string]*breaker // by export

	// Test seams, nil in production: beforeCall runs on the instance about to
	// be called, afterCall right after the call with the per-call cancel.
	beforeCall func(*extism.Plugin)
	afterCall  func(context.CancelFunc)

	logMu     sync.Mutex
	logTokens float64
	logLast   time.Time
	logMuted  bool
}

// breaker counts consecutive traps and timeouts of one export.
type breaker struct {
	fails     int
	openUntil time.Time
}

// allow reports whether export may run now. A closed breaker always allows;
// an open one refuses until its cool-down ends, then lets calls probe again
// (one more failure re-opens it at once).
func (p *plugin) allow(export string) bool {
	p.brkMu.Lock()
	defer p.brkMu.Unlock()
	b := p.breakers[export]
	return b == nil || !time.Now().Before(b.openUntil)
}

func (p *plugin) succeeded(export string) {
	p.brkMu.Lock()
	defer p.brkMu.Unlock()
	delete(p.breakers, export)
}

func (p *plugin) failed(export string) {
	p.brkMu.Lock()
	defer p.brkMu.Unlock()
	if p.breakers == nil {
		p.breakers = map[string]*breaker{}
	}
	b := p.breakers[export]
	if b == nil {
		b = &breaker{}
		p.breakers[export] = b
	}
	b.fails++
	if b.fails >= breakerThreshold {
		if !time.Now().Before(b.openUntil) {
			p.log.Error("plugin hook disabled after repeated failures",
				slog.String("export", export), slog.Int("failures", b.fails), slog.Duration("cooldown", breakerCooldown))
		}
		b.openUntil = time.Now().Add(breakerCooldown)
	}
}

// guestLog forwards one guest log line, truncated and rate limited so a
// chatty guest cannot flood the host's log.
func (p *plugin) guestLog(level extism.LogLevel, msg string) {
	now := time.Now()
	p.logMu.Lock()
	if p.logLast.IsZero() {
		p.logTokens = logBurst
	} else {
		p.logTokens = min(logBurst, p.logTokens+now.Sub(p.logLast).Seconds()*logPerSec)
	}
	p.logLast = now
	ok := p.logTokens >= 1
	first := !ok && !p.logMuted
	if ok {
		p.logTokens--
	}
	p.logMuted = !ok
	p.logMu.Unlock()
	if first {
		p.log.Warn("plugin log output suppressed (rate limit)")
	}
	if !ok {
		return
	}
	if len(msg) > maxLogBytes {
		msg = msg[:maxLogBytes] + "...(truncated)"
	}
	p.log.Log(context.Background(), slogLevel(level), msg)
}

// Load compiles wasm and places the plugin under m.Name, replacing a plugin
// of the same name once the old one's in-flight calls have finished. The
// manifest is validated and defaulted here; a module that cannot be
// compiled and instantiated fails the load. A Go guest's init() runs on the
// first call, not here, so a panicking init() fails every call instead.
func (h *Host) Load(ctx context.Context, m sdk.Manifest, wasm []byte) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return h.load(ctx, m, wasm)
}

// load is Load for a manifest that is already validated.
func (h *Host) load(ctx context.Context, m sdk.Manifest, wasm []byte) error {
	m = m.WithDefaults()
	manifest := extism.Manifest{
		Wasm: []extism.Wasm{extism.WasmData{Data: wasm, Name: "main"}},
		// MaxPages bounds every module of the runtime (kernel and guest), so
		// the real ceiling per instance is about twice MemoryPages.
		Memory: &extism.ManifestMemory{
			MaxPages:             m.Limits.MemoryPages,
			MaxHttpResponseBytes: maxHTTPResponseBytes,
			MaxVarBytes:          maxVarBytes,
		},
		Config: m.Config,
		// No network: Extism's http_request would follow redirects to hosts
		// outside the list (it uses http.DefaultClient). m.AllowedHosts feeds
		// the guarded http.fetch host function and is deliberately NOT
		// passed on.
		AllowedHosts: nil,
		AllowedPaths: nil, // no filesystem
		// Extism enables its deadline checks (CloseOnContextDone) when this
		// is non-zero and bounds every call by it; each call also gets its
		// hook's own, shorter, deadline (see Call).
		Timeout: sdk.MaxTimeoutMS,
	}
	funcs := []extism.HostFunction{h.hostFunction(m.Name, h.log.With(slog.String("plugin", m.Name)))}
	compiled, err := extism.NewCompiledPlugin(ctx, manifest, extism.PluginConfig{
		EnableWasi:    true, // Go wasip1 needs clock, random and proc_exit; nothing else is mounted
		RuntimeConfig: wazero.NewRuntimeConfig().WithCompilationCache(h.cache),
	}, funcs)
	if err != nil {
		return fmt.Errorf("wasmhost: compile %s: %w", m.Name, err)
	}
	p := &plugin{
		manifest: m,
		compiled: compiled,
		pool:     make(chan *extism.Plugin, m.Limits.MaxInstances),
		slots:    make(chan struct{}, m.Limits.MaxInstances),
		log:      h.log.With(slog.String("plugin", m.Name)),
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		_ = compiled.Close(ctx)
		return fmt.Errorf("wasmhost: instantiate %s: %w", m.Name, err)
	}
	p.pool <- inst

	h.mu.Lock()
	old := *h.reg.Load()
	next := make(map[string]*plugin, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	prev := next[m.Name]
	next[m.Name] = p
	h.reg.Store(&next)
	h.mu.Unlock()

	if prev != nil {
		prev.close(ctx)
	}
	return nil
}

// Unload removes the plugin and frees its runtime once in-flight calls end.
func (h *Host) Unload(ctx context.Context, name string) bool {
	h.mu.Lock()
	old := *h.reg.Load()
	p, ok := old[name]
	if ok {
		next := make(map[string]*plugin, len(old))
		for k, v := range old {
			if k != name {
				next[k] = v
			}
		}
		h.reg.Store(&next)
	}
	h.mu.Unlock()
	if ok {
		p.close(ctx)
	}
	return ok
}

// SetHostCall sets who answers hm_call for plugins loaded from now on.
// Without it every call fails with an "unavailable" error response.
func (h *Host) SetHostCall(fn HostCall) { h.hostCall = fn }

// maxHostCallBytes bounds one guest request to hm_call.
const maxHostCallBytes = 1 << 20

// hostFunction builds the hm_call import of one plugin.
func (h *Host) hostFunction(name string, log *slog.Logger) extism.HostFunction {
	fail := func(code, msg string) []byte {
		b, _ := json.Marshal(sdk.CallResponse{Error: &sdk.HostError{Code: code, Message: msg}})
		return b
	}
	call := h.hostCall
	if call == nil {
		call = func(context.Context, string, []byte) []byte { return fail(sdk.ErrUnavailable, "no host functions") }
	}
	return extism.NewHostFunctionWithStack(sdk.HostFunc,
		func(ctx context.Context, cp *extism.CurrentPlugin, stack []uint64) {
			var out []byte
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error("host call panicked", slog.Any("panic", r))
						out = fail(sdk.ErrInternal, "host call failed")
					}
				}()
				in, err := cp.ReadBytes(stack[0])
				switch {
				case err != nil:
					out = fail(sdk.ErrInvalidArgument, "unreadable request")
				case len(in) > maxHostCallBytes:
					out = fail(sdk.ErrInvalidArgument, "request too large")
				default:
					out = call(ctx, name, in)
				}
			}()
			off, err := cp.WriteBytes(out)
			if err != nil {
				log.Error("host call: write response", slog.Any("error", err))
				off = 0
			}
			stack[0] = off
		},
		[]extism.ValueType{extism.ValueTypePTR}, []extism.ValueType{extism.ValueTypePTR})
}

// Close unloads every plugin and the compilation cache.
func (h *Host) Close(ctx context.Context) {
	for _, name := range h.Loaded() {
		h.Unload(ctx, name)
	}
	_ = h.cache.Close(ctx)
}

// Loaded reports the names currently loaded, sorted.
func (h *Host) Loaded() []string {
	var out []string
	for k := range *h.reg.Load() {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Discarded reports how many instances of name were dropped after failures
// (-1 when the plugin is not loaded).
func (h *Host) Discarded(name string) int64 {
	if p := (*h.reg.Load())[name]; p != nil {
		return p.discarded.Load()
	}
	return -1
}

// HasExport reports whether the plugin exports a function called export.
func (h *Host) HasExport(name, export string) bool {
	p := (*h.reg.Load())[name]
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case inst := <-p.pool:
		ok := inst.FunctionExists(export)
		p.pool <- inst
		return ok
	default:
	}
	inst, err := p.newInstance(context.Background())
	if err != nil {
		return false
	}
	defer inst.Close(context.Background())
	return inst.FunctionExists(export)
}

func (p *plugin) close(ctx context.Context) {
	p.mu.Lock() // waits for in-flight calls, each bounded by the timeout
	defer p.mu.Unlock()
	p.closed = true
	for {
		select {
		case inst := <-p.pool:
			_ = inst.Close(ctx)
		default:
			_ = p.compiled.Close(ctx) // closes the wazero runtime and every module
			return
		}
	}
}

func (p *plugin) newInstance(ctx context.Context) (*extism.Plugin, error) {
	inst, err := p.compiled.Instance(ctx, extism.PluginInstanceConfig{
		// No FS, env or args; stdout and stderr are discarded.
		ModuleConfig: wazero.NewModuleConfig().WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader),
	})
	if err != nil {
		return nil, err
	}
	inst.SetLogger(p.guestLog)
	return inst, nil
}

func slogLevel(l extism.LogLevel) slog.Level {
	switch l {
	case extism.LogLevelDebug, extism.LogLevelTrace:
		return slog.LevelDebug
	case extism.LogLevelWarn:
		return slog.LevelWarn
	case extism.LogLevelError:
		return slog.LevelError
	}
	return slog.LevelInfo
}

// acquire returns the plugin registered as name with its read lock held. A
// plugin that a reload replaced between the registry lookup and the lock is
// closed: look again, so a swap never fails a call.
func (h *Host) acquire(name string) (*plugin, error) {
	for {
		p := (*h.reg.Load())[name]
		if p == nil {
			return nil, ErrNotLoaded
		}
		p.mu.RLock()
		if !p.closed {
			return p, nil
		}
		p.mu.RUnlock() // closed plugins are already out of the registry: the next lookup differs
	}
}

// Call runs export on plugin name with JSON input and returns its output.
//
// A trap, timeout, out-of-memory or guest error returns an error and discards
// the instance that saw it; only a deliberate rejection (sdk.RejectPrefix)
// keeps its instance. Calls beyond the plugin's max_instances wait for a free
// slot until ctx ends or slotWait passes, then fail with ErrSaturated (the
// instances are untouched), so memory use stays inside
// max_instances x memory_pages x 2. The hook's timeout covers only the guest's
// run: neither the slot wait nor a cold start (at most maxStartWait) is charged
// to it, so the worst latency without a caller deadline is
// slotWait + maxStartWait + the hook's timeout. A caller deadline shortens
// all three together: the wait, the start and the run share it, so hooks
// called with one (render.head, jsonld.enrich) can still be cut short by it;
// that cut is not counted against the circuit breaker when our own waiting
// (slot wait plus cold start) was what shortened the guest's run; a guest cut
// by a short caller deadline that it had to itself is charged as usual.
func (h *Host) Call(ctx context.Context, name, export string, in []byte) ([]byte, error) {
	entry := time.Now()
	p, err := h.acquire(name)
	if err != nil {
		return nil, err
	}
	defer p.mu.RUnlock()
	if !p.allow(export) {
		return nil, fmt.Errorf("%w: %s.%s", ErrCircuitOpen, name, export)
	}

	// Three separate bounds, so time spent getting ready never eats the
	// guest's own deadline (and a plugin that is merely busy does not lose a
	// healthy instance to a timeout it did not cause):
	//   1. the wait for a free slot: at most slotWait(export, timeout), and the
	//      caller's deadline, so a deadline-less ctx cannot queue forever;
	//   2. starting a cold instance: at most maxStartWait, and the caller's
	//      deadline;
	//   3. the guest itself: the hook's timeout, started only once an instance
	//      is in hand.
	// Worst case without a caller deadline: slotWait + maxStartWait + timeout.
	timeout := p.manifest.HookTimeout(export)
	wait := time.NewTimer(slotWait(export, timeout))
	defer wait.Stop()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %s: %w", ErrSaturated, name, ctx.Err())
	case <-wait.C:
		return nil, fmt.Errorf("%w: %s: no free instance within %s", ErrSaturated, name, slotWait(export, timeout))
	}

	var inst *extism.Plugin
	select {
	case inst = <-p.pool:
	default:
		startCtx, cancelStart := context.WithTimeout(ctx, maxStartWait)
		var err error
		inst, err = p.newInstance(startCtx)
		cancelStart()
		if err != nil {
			if ctxErr := startCtx.Err(); ctxErr != nil {
				// The deadline ran out while the instance was starting:
				// report it as the timeout it is, not as a broken module.
				return nil, fmt.Errorf("%s.%s: starting an instance: %w", name, export, ctxErr)
			}
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		// The caller gave up while we waited; the instance never ran, so it
		// is healthy: keep it.
		p.release(inst)
		return nil, fmt.Errorf("%s.%s: caller gave up before the call: %w", name, export, err)
	}
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(callerCtx, timeout)
	defer cancel()
	cancelCall := cancel // the current attempt's; replaced on retry

	var (
		start   time.Time
		rc      uint32
		out     []byte
		callErr error
	)
	run := func() {
		if p.beforeCall != nil {
			p.beforeCall(inst)
		}
		start = time.Now()
		rc, out, callErr = inst.CallWithContext(ctx, export, in)
		if p.afterCall != nil {
			p.afterCall(cancelCall)
		}
	}
	run()
	if isClosedErr(callErr) && !isResetErr(callErr) {
		// The instance never ran (the check is the first thing the call
		// does), so one retry on a fresh instance is safe. The slot stays
		// held across it.
		p.discardClosed(export, inst)
		startCtx, cancelStart := context.WithTimeout(callerCtx, maxStartWait)
		var err error
		inst, err = p.newInstance(startCtx)
		cancelStart()
		if err != nil {
			if ctxErr := startCtx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("%s.%s: starting an instance: %w", name, export, ctxErr)
			}
			return nil, err
		}
		// The cold start must not eat the hook's own timeout.
		cancel()
		var cancelRetry context.CancelFunc
		ctx, cancelRetry = context.WithTimeout(callerCtx, timeout)
		defer cancelRetry()
		cancelCall = cancelRetry
		run()
	}
	err = callErr
	if isClosedErr(err) {
		p.discardClosed(export, inst)
		return nil, fmt.Errorf("%s.%s: %w", name, export, err)
	}
	if err == nil && rc != 0 {
		err = fmt.Errorf("%s.%s returned %d", name, export, rc)
	}
	if err == nil && len(out) > int(p.manifest.Limits.MaxOutputBytes) {
		err = fmt.Errorf("%w: %d bytes (max %d)", ErrOutputTooLarge, len(out), p.manifest.Limits.MaxOutputBytes)
	}
	if err != nil {
		reject := strings.HasPrefix(err.Error(), sdk.RejectPrefix)
		if reject || isGuestError(err) {
			// The guest ran to the end and reported a failure through the
			// PDK: its runtime is healthy and the plugin is not charged. The
			// instance is pooled only if the deadline has not passed, since
			// wazero may be closing the module of a done context.
			p.succeeded(export)
			p.releaseUnlessDone(ctx, inst)
			if reject {
				return nil, err
			}
			return nil, fmt.Errorf("%s.%s: %w", name, export, err)
		}
		// A caller deadline (render.head, jsonld.enrich) that cut the guest
		// short is not the plugin's fault only if our own waiting (slot wait
		// plus cold start) is what left it so little time: it ran for less
		// than it waited. A guest that had the time to itself and still ran
		// into the deadline (a hang) is charged, so it cannot dodge the
		// breaker by declaring a timeout longer than its caller's. The
		// interrupted instance is discarded either way.
		ran := time.Since(start)
		if callerCtx.Err() == nil || ran >= timeout || ran >= start.Sub(entry) {
			p.failed(export)
		}
		p.discarded.Add(1)
		_ = inst.Close(context.Background())
		return nil, fmt.Errorf("%s.%s after %s: %w", name, export, ran.Round(time.Millisecond), err)
	}
	p.succeeded(export)
	p.releaseUnlessDone(ctx, inst)
	return out, nil
}

// releaseUnlessDone pools inst, or discards it when ctx is done: a deadline
// that hit at the tail of the call may have had wazero close the module.
func (p *plugin) releaseUnlessDone(ctx context.Context, inst *extism.Plugin) {
	if ctx.Err() != nil {
		p.discarded.Add(1)
		_ = inst.Close(context.Background())
		return
	}
	p.release(inst)
}

// release returns inst to the pool, or closes it when the pool is full. It
// closes with a background context: a per-call context may already be done.
func (p *plugin) release(inst *extism.Plugin) {
	select {
	case p.pool <- inst:
	default:
		_ = inst.Close(context.Background())
	}
}

// discardClosed drops an instance whose module is already closed.
func (p *plugin) discardClosed(export string, inst *extism.Plugin) {
	p.discarded.Add(1)
	_ = inst.Close(context.Background())
	p.log.Warn("wasmhost: discarded closed plugin instance", slog.String("export", export))
}

// isClosedErr reports the two plain errors extism go-sdk v1.7.1 raises when a
// module of the instance is closed: "module is closed" (extism.go:454-455,
// the main module, the first thing CallWithContext checks) and "reset"
// (extism.go:372-375, SetInput on a closed kernel module, before f.Call). The
// SDK has no IsClosed, so the text is the only handle; a bump must keep the
// closed-instance test green.
func isClosedErr(err error) bool {
	return err != nil && (err.Error() == "module is closed" || isResetErr(err))
}

// isResetErr matches "reset" alone. Unlike "module is closed" a guest can
// also report that text through the PDK, so it is not provably pre-guest:
// the instance is discarded but the call is not retried.
func isResetErr(err error) bool { return err != nil && err.Error() == "reset" }

// plainError is the concrete type of errors.New: Extism reports a guest's PDK
// error message that way, whereas traps, timeouts and exits come back as
// wazero (or wrapped) error types.
var plainError = reflect.TypeOf(errors.New(""))

func isGuestError(err error) bool { return reflect.TypeOf(err) == plainError && !isClosedErr(err) }
