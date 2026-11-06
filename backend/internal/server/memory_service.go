package server

import (
	"context"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/runtimestats"
	"hatamoto.dev/core/internal/wasmhost"
)

// memoryService implements admin.MemoryService from the Go runtime, the page
// cache and the plugin host. The page cache and plugin host are read at call
// time: either can be nil, and the plugins are set up after the admin mounts.
type memoryService struct{ s *Server }

func (m memoryService) Snapshot(ctx context.Context) (admin.MemoryView, error) {
	rt := runtimestats.Read()
	v := admin.MemoryView{
		Runtime: admin.RuntimeMemory{
			LiveHeap:   int64(rt.LiveHeap),
			TotalHeap:  int64(rt.TotalHeap),
			MemLimit:   rt.MemLimit,
			LimitSet:   rt.LimitSet,
			GCCPUShare: rt.GCCPUShare,
			Goroutines: rt.Goroutines,
		},
		ReadAt: time.Now(),
	}
	if m.s.pageCache != nil {
		st := m.s.pageCache.Stats(ctx)
		v.PageCache = &st
	}
	if m.s.plugins != nil {
		addHostStats(&v, m.s.plugins.MemoryStats())
	}
	return v, nil
}

// addHostStats copies the plugin host's numbers into v.
func addHostStats(v *admin.MemoryView, hs wasmhost.HostStats) {
	v.PluginsAvailable = true
	v.CacheOnDisk = hs.CacheOnDisk
	v.CacheBytes = hs.CacheBytes
	v.Plugins = make([]admin.PluginMemoryRow, 0, len(hs.Plugins))
	for _, p := range hs.Plugins {
		v.Plugins = append(v.Plugins, admin.PluginMemoryRow{
			Name:        p.Name,
			Pooled:      p.Pooled,
			Live:        p.Live,
			WasmBytes:   int64(p.PooledWasmBytes),
			ModuleBytes: p.ModuleBytes,
		})
	}
}
