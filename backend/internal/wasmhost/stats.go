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

// Stats reports the loaded plugins' pools. It is lock-free: it takes no slot,
// pops nothing from a pool and reads no instance, only numbers recorded by
// the instances' owners (see pushPooled).
func (h *Host) Stats() HostStats {
	reg := *h.reg.Load()
	st := HostStats{Plugins: make([]PluginStats, 0, len(reg)), Evicted: h.evicted.Load(), IdleWindow: h.idleWindow}
	for name, p := range reg {
		ps := PluginStats{
			Name:         name,
			Pooled:       len(p.pool),
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

// pushPooled records inst's memory, then pools it. Only the owner of inst
// (the goroutine that just finished with it) may call it, so the instance is
// idle while it is measured. The record is stored BEFORE the send so Stats
// never sees a pooled instance without one. The send blocks when the pool is
// full, so only use it where the pool is known to have room (load, on an
// empty pool). Anyone who holds no slot must use tryPushPooled or release.
func (p *plugin) pushPooled(inst *extism.Plugin) {
	p.pooledMem.Store(inst, pooledRec{instanceBytes(inst), p.now()})
	p.pool <- inst
}

// tryPushPooled is pushPooled that gives up when the pool is full.
func (p *plugin) tryPushPooled(inst *extism.Plugin) bool {
	p.pooledMem.Store(inst, pooledRec{instanceBytes(inst), p.now()})
	select {
	case p.pool <- inst:
		return true
	default:
		p.pooledMem.Delete(inst)
		return false
	}
}

// pooledRec is what pooledMem keeps per pooled instance: its measured memory
// and when it was returned. The pool is FIFO, so the time is per instance: a
// per-plugin clock would keep stale instances alive.
type pooledRec struct {
	bytes uint64
	at    time.Time
}

// sweepIdle closes pooled instances that have sat unused for at least window,
// always leaving one (the most recently returned) and never starting one. It
// holds no slot and never blocks: it pops from the pool without waiting, so
// an instance it holds is exclusively its own, and a caller that finds the
// pool empty at worst cold-starts. RLock makes close wait for the sweep and
// makes a closed plugin a no-op. It returns the number of instances closed.
func (p *plugin) sweepIdle(now time.Time, window time.Duration) (closed int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return 0
	}
	// One pass over the instances present now: stale ones are closed, fresh
	// ones go back to the tail, so a full pass leaves the FIFO order intact.
	for range len(p.pool) {
		if len(p.pool) <= 1 {
			return closed
		}
		var inst *extism.Plugin
		select {
		case inst = <-p.pool:
		default:
			return closed
		}
		v, _ := p.pooledMem.Load(inst)
		rec, _ := v.(pooledRec)
		// An empty pool means a caller took the sibling, so this one is the
		// last warm instance: keep it, however old.
		if now.Sub(rec.at) < window || len(p.pool) == 0 {
			p.putBack(inst, rec)
			continue
		}
		p.popPooled(inst) // forget it BEFORE closing, so Stats never counts a closed instance
		_ = inst.Close(context.Background())
		closed++
	}
	return closed
}

// putBack returns an instance sweepIdle took, keeping its original return
// time. It never blocks: when callers refilled the pool meanwhile it closes
// the instance instead, like release.
func (p *plugin) putBack(inst *extism.Plugin, rec pooledRec) {
	p.pooledMem.Store(inst, rec)
	select {
	case p.pool <- inst:
	default:
		p.popPooled(inst)
		_ = inst.Close(context.Background())
	}
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
	if m := inst.Memory(); m != nil {
		n += uint64(m.Size())
	}
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
	if mem := mod.Memory(); mem != nil {
		return uint64(mem.Size())
	}
	return 0
}
