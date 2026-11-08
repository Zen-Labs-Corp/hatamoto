package wasmhost

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unsafe"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero/api"
)

// PluginStats is a point-in-time view of one loaded plugin's instances.
type PluginStats struct {
	Name         string
	Pooled       int // idle instances in the pool
	Live         int // calls holding a slot right now
	MaxInstances int
	// PooledWasmBytes is the linear memory of the pooled instances only
	// (kernel plus guest). Instances running a call are never read.
	PooledWasmBytes uint64
	// ModuleBytes is the size of the retained .wasm module, the measure of
	// compiled code per plugin (wazero reports no per-module cache size).
	ModuleBytes int64
	Discarded   int64
}

// HostStats is a point-in-time view of the host.
type HostStats struct {
	CacheOnDisk bool
	// CacheBytes is the size of the compilation cache on disk, shared by all
	// plugins; 0 when the cache is in memory.
	CacheBytes int64
	Plugins    []PluginStats // sorted by name
	// Evicted counts instances closed for idleness since the host started
	// (not failures: see PluginStats.Discarded).
	Evicted int64
	// IdleWindow is how long a pooled instance may sit unused before it is
	// closed, down to one warm instance per plugin.
	IdleWindow time.Duration
}

// Stats reports the loaded plugins' pools. It takes no slot or plugin lock,
// pops nothing from a pool (only a brief leaf lock to count it) and reads no
// instance, only numbers recorded by the instances' owners (see
// tryPushPooled).
func (h *Host) Stats() HostStats {
	reg := *h.reg.Load()
	st := HostStats{Plugins: make([]PluginStats, 0, len(reg)), Evicted: h.evicted.Load(), IdleWindow: h.idleWindow}
	for name, p := range reg {
		ps := PluginStats{
			Name:         name,
			Pooled:       p.poolLen(),
			Live:         len(p.slots),
			MaxInstances: cap(p.slots),
			ModuleBytes:  p.wasmBytes,
			Discarded:    p.discarded.Load(),
		}
		p.pooledMem.Range(func(_, v any) bool {
			ps.PooledWasmBytes += v.(pooledRec).bytes
			return true
		})
		st.Plugins = append(st.Plugins, ps)
	}
	slices.SortFunc(st.Plugins, func(a, b PluginStats) int { return strings.Compare(a.Name, b.Name) })
	if h.cacheDir != "" {
		st.CacheOnDisk = true
		st.CacheBytes = dirCacheBytes(h.cacheDir)
	}
	return st
}

// dirCacheBytes sums the compiled-code files wazero keeps in its versioned
// subdirectory of dir, skipping half-written *.tmp files.
func dirCacheBytes(dir string) int64 {
	subs, _ := filepath.Glob(filepath.Join(dir, "wazero-*-*"))
	var total int64
	for _, sub := range subs {
		_ = filepath.WalkDir(sub, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), ".tmp") {
				return nil
			}
			if info, err := os.Stat(path); err == nil {
				total += info.Size()
			}
			return nil
		})
	}
	return total
}

// tryPushPooled records inst's memory, then pools it as the newest instance;
// it gives up (false) when the pool is full. Only the owner of inst (the
// goroutine that just finished with it) may call it, so the instance is idle
// while it is measured. The record is stored BEFORE the push so Stats never
// sees a pooled instance without one. It never blocks.
func (p *plugin) tryPushPooled(inst *extism.Plugin) bool {
	p.pooledMem.Store(inst, pooledRec{instanceBytes(inst), p.now()})
	p.pmu.Lock()
	if len(p.idle) >= cap(p.slots) {
		p.pmu.Unlock()
		p.pooledMem.Delete(inst)
		return false
	}
	p.idle = append(p.idle, inst)
	p.pmu.Unlock()
	return true
}

// popTop takes the most recently returned instance and forgets its record,
// or returns nil when the pool is empty. It never blocks.
func (p *plugin) popTop() *extism.Plugin {
	p.pmu.Lock()
	n := len(p.idle)
	if n == 0 {
		p.pmu.Unlock()
		return nil
	}
	inst := p.idle[n-1]
	p.idle[n-1] = nil
	p.idle = p.idle[:n-1]
	p.pmu.Unlock()
	p.popPooled(inst)
	return inst
}

// poolLen is the number of pooled instances, a snapshot.
func (p *plugin) poolLen() int {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	return len(p.idle)
}

// pooledRec is what pooledMem keeps per pooled instance: its measured memory
// and when it was returned. The time is per instance, not per pool: a
// per-plugin clock would keep stale instances alive.
type pooledRec struct {
	bytes uint64
	at    time.Time
}

// sweepIdle closes pooled instances that have sat unused for at least window,
// oldest first, always leaving the newest and never starting one. It holds no
// slot and never blocks: it filters the stack in place under the leaf lock
// (survivors keep their order, the newest stays on top) and closes the stale
// ones outside it, after they have left the pool, so they are exclusively its
// own. A caller that finds the pool empty at worst cold-starts. RLock makes
// close wait for the sweep and makes a closed plugin a no-op. It returns the
// number of instances closed.
func (p *plugin) sweepIdle(now time.Time, window time.Duration) (closed int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return 0
	}
	var stale []*extism.Plugin
	p.pmu.Lock()
	n := len(p.idle)
	keep := p.idle[:0]
	for i, inst := range p.idle {
		v, _ := p.pooledMem.Load(inst)
		rec, _ := v.(pooledRec)
		// The top is the last warm instance: keep it, however old.
		if i < n-1 && now.Sub(rec.at) >= window {
			stale = append(stale, inst)
		} else {
			keep = append(keep, inst)
		}
	}
	clear(p.idle[len(keep):]) // let the closed ones be collected
	p.idle = keep
	p.pmu.Unlock()
	for _, inst := range stale {
		p.popPooled(inst) // forget it BEFORE closing, so Stats never counts a closed instance
		_ = inst.Close(context.Background())
		closed++
	}
	return closed
}

// popPooled drops the record of an instance just received from the pool.
func (p *plugin) popPooled(inst *extism.Plugin) { p.pooledMem.Delete(inst) }

// instanceBytes is the linear memory of inst: the Extism kernel's plus the
// guest's. The public API (Plugin.Memory) exposes only the kernel's; the
// guest module is the unexported field mainModule of extism go-sdk v1.7.1
// (extism.go:116). Version-pinned: TestInstanceBytesGuestIncluded fails on a
// bump that moves it. If the field is missing the result is the kernel's
// alone (understated, never wrong in kind). inst must not be running a call.
func instanceBytes(inst *extism.Plugin) uint64 {
	var n uint64
	n += memorySize(inst.Memory())
	return n + guestMemoryBytes(inst)
}

// guestMemoryBytes reads the guest module's memory size, or 0 when the SDK
// no longer has the field the pin expects.
func guestMemoryBytes(inst *extism.Plugin) uint64 {
	f := reflect.ValueOf(inst).Elem().FieldByName("mainModule")
	if !f.IsValid() || f.Type() != reflect.TypeFor[api.Module]() || !f.CanAddr() {
		return 0
	}
	mod, _ := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(api.Module)
	if mod == nil {
		return 0
	}
	return memorySize(mod.Memory())
}

// memorySize returns a memory's size in bytes, or 0 when there is none.
// wazero returns a non-nil api.Memory holding a nil *MemoryInstance for a
// module without memory, and calling Size on it panics, so a plain nil check
// is not enough.
func memorySize(m api.Memory) uint64 {
	if m == nil {
		return 0
	}
	if v := reflect.ValueOf(m); v.Kind() == reflect.Pointer && v.IsNil() {
		return 0
	}
	return uint64(m.Size())
}
