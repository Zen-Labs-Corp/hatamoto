package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

type fakeMemory struct {
	view MemoryView
	err  error
}

func (f *fakeMemory) Snapshot(context.Context) (MemoryView, error) { return f.view, f.err }

func newMemoryView() MemoryView {
	return MemoryView{
		Runtime: RuntimeMemory{LiveHeap: 212 << 20, TotalHeap: 341 << 20, GCCPUShare: 0.018, Goroutines: 143},
		PageCache: &PageCacheStats{
			Enabled: true, Pages: 1204, Bytes: 48 << 20, CapBytes: 64 << 20,
		},
		Plugins: []PluginMemoryRow{
			{Name: "event-calendar", Pooled: 1, WasmBytes: 4300000, ModuleBytes: 2400000},
			{Name: "reviews", Pooled: 4, Live: 1, WasmBytes: 71 << 20, ModuleBytes: 6 << 20},
			{Name: "alt-text", ModuleBytes: 1100000},
		},
		PluginsAvailable: true,
		ReadAt:           time.Date(2026, 11, 7, 10, 42, 7, 0, time.UTC),
	}
}

func memoryRig(t *testing.T, v MemoryView) *ownerRig {
	t.Helper()
	g := newOwnerRig(t, true)
	g.a.deps.Memory = &fakeMemory{view: v}
	return g
}

func getMemory(t *testing.T, g *ownerRig) string {
	t.Helper()
	rec := g.do("GET", "/admin/settings/performance/memory", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestMemoryPage(t *testing.T) {
	t.Parallel()

	body := getMemory(t, memoryRig(t, newMemoryView()))
	for _, want := range []string{"Live heap", "212.0 MB", "Total heap", "341.0 MB", "Memory limit", "GC CPU share", "1.8 %", "Goroutines", "143",
		"48.0 MB of 64.0 MB", "1,204 pages", "event-calendar", "reviews", "alt-text", "Total", `href="/admin/settings/performance"`, `href="/admin/settings/performance/memory"`, "Refresh"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<form") && strings.Contains(body, `action="/admin/settings/performance/memory"`) {
		t.Error("memory page has a form posting to itself")
	}
	compareGolden(t, "pages/settings-performance-memory.html", normalizePage(body))
}

func TestMemoryOwnerOnlyAndUnavailable(t *testing.T) {
	t.Parallel()

	g := memoryRig(t, newMemoryView())
	if rec := g.do("GET", "/admin/settings/performance/memory", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/performance/memory", "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d, want 303", rec.Code)
	}
	g.a.deps.Memory = nil
	if rec := g.do("GET", "/admin/settings/performance/memory", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("nil service GET = %d, want 503", rec.Code)
	}
	g.a.deps.Memory = &fakeMemory{err: errors.New("boom")}
	if rec := g.do("GET", "/admin/settings/performance/memory", "owner-token", nil, nil); rec.Code != 500 {
		t.Errorf("failing service GET = %d, want 500", rec.Code)
	}
}

func TestMemoryCompiledLabel(t *testing.T) {
	t.Parallel()

	v := newMemoryView()
	body := getMemory(t, memoryRig(t, v))
	if !strings.Contains(body, "Compiled code (module size)") || !strings.Contains(body, "pooled instances only") {
		t.Error("column headers do not state their measure")
	}
	if strings.Contains(body, "Compilation cache on disk") {
		t.Error("disk footer shown without an on-disk cache")
	}
	v.CacheOnDisk, v.CacheBytes = true, 9<<20
	body = getMemory(t, memoryRig(t, v))
	if !strings.Contains(body, "Compilation cache on disk: 9.0 MB, shared by all plugins.") {
		t.Error("disk footer missing with an on-disk cache")
	}
}

func TestMemoryLimit(t *testing.T) {
	t.Parallel()

	v := newMemoryView()
	if body := getMemory(t, memoryRig(t, v)); !strings.Contains(body, "not set") {
		t.Error("unset limit not shown as not set")
	}
	v.Runtime.LimitSet, v.Runtime.MemLimit = true, 512<<20
	body := getMemory(t, memoryRig(t, v))
	if !strings.Contains(body, "512.0 MB") || strings.Contains(body, "not set") {
		t.Error("set limit not shown")
	}
}

func TestMemoryNeverCalledPlugin(t *testing.T) {
	t.Parallel()

	tbl := memoryPluginTable(newMemoryView().Plugins)
	idle := tbl.Rows[2].Cells
	if idle[0].Text != "alt-text" || idle[1].Text != "0" || idle[2].Text != "0" || idle[3].Text != "-" || idle[4].Text != "1.0 MB" {
		t.Errorf("never-called row = %+v", idle)
	}
	total := tbl.Rows[3].Cells
	if total[0].Text != "Total" || total[1].Text != "5" || total[2].Text != "1" {
		t.Errorf("total row = %+v", total)
	}
}

func TestMemoryPageCacheAndPluginsOff(t *testing.T) {
	t.Parallel()

	body := getMemory(t, memoryRig(t, MemoryView{ReadAt: time.Now()}))
	for _, want := range []string{"The page cache is not available on this server.", "The plugin host is not running"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestMemoryPluginsHeading(t *testing.T) {
	t.Parallel()

	body := getMemory(t, memoryRig(t, newMemoryView()))
	if !strings.Contains(body, `id="memory-plugins-card"`) || !strings.Contains(body, ">Plugins</h2>") {
		t.Error("plugins table has no visible Plugins heading")
	}
}

func TestMemoryPageCacheDisabled(t *testing.T) {
	t.Parallel()

	v := newMemoryView()
	v.PageCache = &PageCacheStats{Enabled: false, CapBytes: 64 << 20}
	body := getMemory(t, memoryRig(t, v))
	if !strings.Contains(body, "The page cache is off") || strings.Contains(body, "0 pages") {
		t.Error("disabled page cache not shown as off")
	}
}
