package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// MemoryService is the read-only memory snapshot behind Settings > Performance
// > Memory. The server implements it from the Go runtime, the page cache and
// the plugin host.
type MemoryService interface {
	Snapshot(ctx context.Context) (MemoryView, error)
}

// RuntimeMemory is the Go runtime's own account of its memory. LimitSet is
// false when no memory limit (GOMEMLIMIT) is in force. GCCPUShare is a
// fraction of 0 to 1 since the process started.
type RuntimeMemory struct {
	LiveHeap, TotalHeap int64
	MemLimit            int64
	LimitSet            bool
	GCCPUShare          float64
	Goroutines          int
}

// PluginMemoryRow is one loaded plugin. WasmBytes counts pooled (idle)
// instances only; ModuleBytes is the size of the plugin's .wasm module.
type PluginMemoryRow struct {
	Name         string
	Pooled, Live int
	WasmBytes    int64
	ModuleBytes  int64
}

// MemoryView is what the Memory tab shows. PageCache is nil when the page
// cache is unavailable; PluginsAvailable is false while the plugin host is
// not running. CacheBytes is the compilation cache on disk, shared by all
// plugins, and only meaningful when CacheOnDisk is set.
type MemoryView struct {
	Runtime          RuntimeMemory
	PageCache        *PageCacheStats
	Plugins          []PluginMemoryRow
	PluginsAvailable bool
	CacheOnDisk      bool
	CacheBytes       int64
	ReadAt           time.Time
}

const performanceMemoryPath = "/settings/performance/memory"

// memoryData is the Memory tab: pre-formatted so the template only lays out.
type memoryData struct {
	Runtime          []memoryRow
	PageCacheLine    string // "" when the page cache is unavailable
	PageCachePages   string
	Plugins          ui.Table
	PluginsAvailable bool
	CacheLine        string // "" unless the compilation cache is on disk
	ReadAt           string
	RefreshHref      string
}

// memoryRow is one name/value row with an explanatory note.
type memoryRow struct{ Name, Value, Note string }

func (a *Admin) handlePerformanceMemory(w http.ResponseWriter, r *http.Request) {
	if a.deps.Memory == nil {
		a.unavailable(w, r, "The memory overview")
		return
	}
	snap, err := a.deps.Memory.Snapshot(r.Context())
	if err != nil {
		a.logger().Error("memory snapshot failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The memory numbers could not be read.")
		return
	}
	v := a.NewView(w, r, "Memory")
	v.Section, v.Tab = "performance", "memory"
	v.Width = "compact"
	v.Data = a.memoryPage(r.Context(), snap)
	a.Render(w, r, http.StatusOK, "settings/performance-memory", v)
}

func (a *Admin) memoryPage(ctx context.Context, s MemoryView) memoryData {
	d := memoryData{
		Runtime:          memoryRuntimeRows(s.Runtime),
		Plugins:          memoryPluginTable(s.Plugins),
		PluginsAvailable: s.PluginsAvailable,
		ReadAt:           s.ReadAt.In(a.siteLocation(ctx)).Format("15:04:05"),
		RefreshHref:      a.adminPath + performanceMemoryPath,
	}
	if pc := s.PageCache; pc != nil {
		d.PageCacheLine = memorySize(pc.Bytes)
		if pc.CapBytes > 0 {
			d.PageCacheLine += " of " + memorySize(pc.CapBytes)
		}
		d.PageCachePages = countNoun(pc.Pages, "page")
	}
	if s.CacheOnDisk {
		d.CacheLine = "Compilation cache on disk: " + memorySize(s.CacheBytes) + ", shared by all plugins."
	}
	return d
}

func memoryRuntimeRows(rt RuntimeMemory) []memoryRow {
	limit := "not set"
	if rt.LimitSet {
		limit = memorySize(rt.MemLimit)
	}
	return []memoryRow{
		{"Live heap", memorySize(rt.LiveHeap), "Memory held by objects in use."},
		{"Total heap", memorySize(rt.TotalHeap), "Heap memory the runtime holds, not counting pages already returned to the OS."},
		{"Memory limit", limit, "GOMEMLIMIT"},
		{"GC CPU share", fmt.Sprintf("%.1f %%", rt.GCCPUShare*100), "Share of CPU time spent on garbage collection since start."},
		{"Goroutines", thousands(rt.Goroutines), ""},
	}
}

func memoryPluginTable(rows []PluginMemoryRow) ui.Table {
	t := ui.Table{
		ID: "memory-plugins", Wrap: true, Caption: "Plugin memory",
		Columns: []ui.Col{
			{Key: "plugin", Label: "Plugin", Role: "title"},
			{Key: "pooled", Label: "Pooled", Role: "num"},
			{Key: "live", Label: "Live", Role: "num"},
			{Key: "wasm", Label: "Wasm memory (pooled instances only)", Role: "num"},
			{Key: "module", Label: "Compiled code (module size)", Role: "num"},
		},
		Empty: ui.Empty{Icon: "database", Title: "No plugins loaded", Body: "Loaded plugins appear here with their instance pool and memory."},
	}
	var total PluginMemoryRow
	for _, p := range rows {
		cells := func(name string, p PluginMemoryRow) []ui.Cell {
			return []ui.Cell{{Text: name}, {Text: strconv.Itoa(p.Pooled)}, {Text: strconv.Itoa(p.Live)},
				{Text: memoryOrDash(p.WasmBytes), Nowrap: true}, {Text: memoryOrDash(p.ModuleBytes), Nowrap: true}}
		}
		t.Rows = append(t.Rows, ui.Row{ID: p.Name, Label: p.Name, Cells: cells(p.Name, p)})
		total.Pooled += p.Pooled
		total.Live += p.Live
		total.WasmBytes += p.WasmBytes
		total.ModuleBytes += p.ModuleBytes
	}
	if len(rows) > 0 {
		t.Rows = append(t.Rows, ui.Row{ID: "total", Label: "Total", Cells: []ui.Cell{{Text: "Total"}, {Text: strconv.Itoa(total.Pooled)}, {Text: strconv.Itoa(total.Live)},
			{Text: memoryOrDash(total.WasmBytes), Nowrap: true}, {Text: memoryOrDash(total.ModuleBytes), Nowrap: true}}})
	}
	return t
}

// memoryOrDash is a size, or "-" for nothing to report.
func memoryOrDash(b int64) string {
	if b <= 0 {
		return "-"
	}
	return memorySize(b)
}

// memorySize is a byte count with one decimal in the largest fitting unit:
// "4.1 MB", "512.0 KB".
func memorySize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	}
	return strconv.FormatInt(b, 10) + " B"
}
