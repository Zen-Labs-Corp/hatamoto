package server

import (
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/wasmhost"
)

func TestMemorySnapshotNoPlugins(t *testing.T) {
	v, err := memoryService{s: &Server{}}.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.PluginsAvailable || len(v.Plugins) != 0 {
		t.Fatalf("plugins reported without a host: %+v", v)
	}
	if v.Runtime.LiveHeap <= 0 || v.Runtime.TotalHeap <= 0 || v.Runtime.Goroutines <= 0 {
		t.Fatalf("runtime numbers missing: %+v", v.Runtime)
	}
	if v.ReadAt.IsZero() {
		t.Fatal("ReadAt not set")
	}
}

func TestMemorySnapshotPageCacheNil(t *testing.T) {
	v, err := memoryService{s: &Server{}}.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.PageCache != nil {
		t.Fatalf("PageCache = %+v, want nil", v.PageCache)
	}
}

func TestMemorySnapshotMapsPlugins(t *testing.T) {
	var v admin.MemoryView
	addHostStats(&v, wasmhost.HostStats{
		CacheOnDisk: true,
		CacheBytes:  4096,
		Evicted:     37,
		IdleWindow:  5 * time.Minute,
		Plugins: []wasmhost.PluginStats{
			{Name: "alpha", Pooled: 2, Live: 1, PooledWasmBytes: 1 << 20, ModuleBytes: 300},
		},
	})
	want := admin.PluginMemoryRow{Name: "alpha", Pooled: 2, Live: 1, WasmBytes: 1 << 20, ModuleBytes: 300}
	if !v.PluginsAvailable || !v.CacheOnDisk || v.CacheBytes != 4096 || v.IdleClosed != 37 || v.IdleWindow != 5*time.Minute || len(v.Plugins) != 1 || v.Plugins[0] != want {
		t.Fatalf("mapping wrong: %+v", v)
	}
}
