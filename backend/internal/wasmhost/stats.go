package wasmhost

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
}

// Stats reports the loaded plugins' pools. It is lock-free: it takes no slot,
// pops nothing from a pool and reads no instance, only numbers recorded by
// the instances' owners (see pushPooled).
func (h *Host) Stats() HostStats {
	reg := *h.reg.Load()
	st := HostStats{Plugins: make([]PluginStats, 0, len(reg))}
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
			ps.PooledWasmBytes += v.(uint64)
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
	p.pooledMem.Store(inst, instanceBytes(inst))
	p.pool <- inst
}

// tryPushPooled is pushPooled that gives up when the pool is full.
func (p *plugin) tryPushPooled(inst *extism.Plugin) bool {
	p.pooledMem.Store(inst, instanceBytes(inst))
	select {
	case p.pool <- inst:
		return true
	default:
		p.pooledMem.Delete(inst)
		return false
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
