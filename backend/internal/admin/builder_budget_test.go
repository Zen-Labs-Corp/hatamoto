package admin

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
)

// The builder budget is split in two. first is the first-paint JS: the static import
// closure of what the page loads eagerly (admin, the module tags, the preloads). total
// adds everything reached through a dynamic import(): lazy chunks (section-form widgets,
// the Tiptap kit, the Style tab). History of the old single ceiling (editor_js_gzip, retired
// by builder-e0-budget-split): 120 -> 128 KiB (admin design pass PR-A: styled Select) ->
// 132 KiB (admin-editors PR-C: phone builder bar), measured 133,592 B when it was split.
const (
	// builderFirstCeil is the acceptance ceiling of the first-paint JS, independent of the
	// budget file's cap (measured + 2 KiB), so a cap edit alone cannot hide growth past it.
	builderFirstCeil = 117 * 1024
	// builderFirstCap only seeds the first-paint cap in the budget file (measured 87,207 B + 2 KiB, E0);
	// the cap stored in admin.json is the one enforced.
	builderFirstCap = 87207 + 2048
	// builderTotalCap is the cap of static + lazy JS: measured 251,655 B + 1 KiB (navigator field-row review fixes, +239 B; it was 251,416 B + 1 KiB, navigator field rows, +1,220 B; before that 249,775 B + 1 KiB, navigator review fixes; it was 248,647 B + 1 KiB, navigator redesign; before that 242,008 B + 3 KiB, SEO drawer).
	builderTotalCap = 251655 + 1024
)

// hmImportRx finds a static or dynamic import of a first-party module. Vendored
// specifiers (@tiptap/*, htmx, sortablejs) never start with @hm/, so they stay out.
var hmImportRx = regexp.MustCompile(`(?:\bfrom|\bimport)\s*\(?\s*["'](@hm/[a-z/]+)["']`)

// hmStaticRx finds static edges only (`from "x"`, `import "x"`), the same shape as the
// asset builder's staticRx: what modulepreload would fetch, so what first paint costs.
var hmStaticRx = regexp.MustCompile(`(?:\bfrom|\bimport)\s*["'](@hm/[a-z/]+)["']`)

// lazyOnly are modules that must stay out of first paint and in the total graph. A static
// import creeping back from a core module, or a builder.html module tag, fails here.
var lazyOnly = []string{"editor/kit", "editor/mount", "content", "repeater", "listlogic", "builder/style", "builder/reuse", "builder/keys", "builder/fields", "builder/presetpanel", "builder/designops", "builder/controls", "builder/controlsui", "builder/boxmodel", "builder/presetsrc", "builder/clipops", "builder/ctxmenu", "builder/banners", "builder/drag", "builder/picker", "builder/history", "builder/historylabel", "builder/addtab", "builder/savedialog", "builder/commandk", "builder/recent", "builder/patterns", "builder/startpanel", "builder/coachmarks", "builder/sitestyles", "builder/colournames", "builder/inlinehtml", "builder/inlinefield", "builder/fieldjump", "builder/elementsettings", "canvas/fmtbar", "canvas/chrome", "canvas/points", "builder/inspcontent", "builder/inspheader", "builder/essentials", "builder/showon", "builder/columnsops", "builder/items", "builder/restoreops", "builder/noderestore", "builder/seodrawer"}

// The builder screen's first-party JS (entry, preloads and everything they
// import, the Tiptap kit modules included, the vendored libraries excluded) stays
// within the budgets. The tables are logged so growth is visible.
func TestBudgetBuilderScreenJS(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	scripts, preloads := moduleTags(builderPage(t, r, builderOnlyDoc))
	entries := append(append([]string{"admin"}, scripts...), preloads...)

	// walk returns the gzip -9 size per module reachable from the entries along rx edges.
	walk := func(rx *regexp.Regexp) map[string]int {
		seen := map[string]int{}
		var visit func(spec string)
		visit = func(spec string) {
			name := strings.TrimPrefix(spec, "@hm/")
			if _, ok := seen[name]; ok {
				return
			}
			u := as.JSURL(name)
			f, ok := as.files[strings.TrimPrefix(u, "/admin/_/")]
			if !ok {
				t.Errorf("module %s is imported but not served", spec)
				return
			}
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(f.body)
			_ = zw.Close()
			seen[name] = buf.Len()
			for _, m := range rx.FindAllSubmatch(f.body, -1) {
				visit(string(m[1]))
			}
		}
		for _, n := range entries {
			visit("@hm/" + n)
		}
		return seen
	}
	sum := func(m map[string]int) (n int) {
		for _, sz := range m {
			n += sz
		}
		return n
	}
	table := func(m map[string]int) string {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return m[names[i]] > m[names[j]] })
		var tbl strings.Builder
		for _, n := range names {
			fmt.Fprintf(&tbl, "\n  %-18s %6d B", n, m[n])
		}
		return tbl.String()
	}

	first, all := walk(hmStaticRx), walk(hmImportRx)
	firstB, totalB := sum(first), sum(all)
	t.Logf("builder first-paint JS (static closure), gzip -9 per file: %d B (%.1f KiB) in %d files%s", firstB, float64(firstB)/1024, len(first), table(first))
	t.Logf("builder total JS (static + lazy), gzip -9 per file: %d B (%.1f KiB) of %d KiB in %d files", totalB, float64(totalB)/1024, builderTotalCap/1024, len(all))
	if firstB > builderFirstCeil {
		t.Errorf("builder first-paint JS is %d B gzipped, over the %d B ceiling", firstB, builderFirstCeil)
	}
	if totalB > builderTotalCap {
		t.Errorf("builder total JS is %d B gzipped, over the %d B budget", totalB, builderTotalCap)
	}
	budgetfile.Check(t, "admin", "editor_js_first_gzip", int64(firstB), budgetfile.Metric{Unit: "bytes", Cap: builderFirstCap, TolAbs: 1024})
	budgetfile.Check(t, "admin", "editor_js_total_gzip", int64(totalB), budgetfile.Metric{Unit: "bytes", Cap: builderTotalCap, TolAbs: 1024})
	budgetfile.Check(t, "admin", "canvas_host_gzip", int64(all["canvas/host"]), budgetfile.Metric{Unit: "bytes", TolAbs: 512})
	for _, must := range []string{"builder/screen", "canvas/host"} {
		if _, ok := first[must]; !ok {
			t.Errorf("%s is not in the first-paint graph: the walk lost a branch", must)
		}
	}
	for _, lazy := range lazyOnly {
		if _, ok := first[lazy]; ok {
			t.Errorf("%s is in the first-paint graph: it must be reached by import() only", lazy)
		}
		if _, ok := all[lazy]; !ok {
			t.Errorf("%s is not in the total graph: the walk lost a dynamic edge", lazy)
		}
	}
}
