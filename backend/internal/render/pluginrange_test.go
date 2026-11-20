package render

import (
	"context"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// mark is printed once per iteration by the budget fixtures. It is a literal
// in template text (never escaped) and does not occur in the page chrome.
const mark = "¤"

// warnRecorder collects the messages of the WARN records it sees.
type warnRecorder struct {
	slog.Handler
	mu   *sync.Mutex
	msgs *[]string
}

func newWarnRecorder() (*slog.Logger, func(contains string) int) {
	h := warnRecorder{Handler: slog.NewTextHandler(nil, nil), mu: &sync.Mutex{}, msgs: new([]string)}
	count := func(contains string) int {
		h.mu.Lock()
		defer h.mu.Unlock()
		n := 0
		for _, m := range *h.msgs {
			if strings.Contains(m, contains) {
				n++
			}
		}
		return n
	}
	return slog.New(h), count
}

func (h warnRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h warnRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h warnRecorder) WithGroup(string) slog.Handler            { return h }
func (h warnRecorder) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelWarn {
		return nil
	}
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	h.mu.Lock()
	*h.msgs = append(*h.msgs, b.String())
	h.mu.Unlock()
	return nil
}

func rangeSection(typ, body string) string {
	return `{{define "section-` + typ + `"}}<p id="` + typ + `">` + body + `</p>{{end}}`
}

func rangeView(types ...string) *PageView {
	v := minimalView()
	for _, typ := range types {
		v.Sections = append(v.Sections, Section{ID: "id-" + typ, Type: typ, Content: map[string]any{
			"items": []any{"a", "b", "c", "d"},
			"faqs":  []any{"x", "y"},
		}})
	}
	return v
}

func mustRegister(t *testing.T, reg *pluginRegistry, plugin string, partials, sections map[string]string) {
	t.Helper()
	if err := reg.registerFunc(plugin, "rows", func(n int) []string { return make([]string, n) }); err != nil {
		t.Fatal(err)
	}
	// spent reports how much of the render's budget has gone: a test probe
	// that runs on the render goroutine, so it reads the bound budget.
	if err := reg.registerFunc(plugin, "spent", func() int { return pluginRangeBudget - int(boundRangeBudget().left) }); err != nil {
		t.Fatal(err)
	}
	for name, src := range partials {
		if err := reg.registerPartial(plugin, name, src); err != nil {
			t.Fatal(err)
		}
	}
	for typ, src := range sections {
		if err := reg.registerSection(plugin, typ, src); err != nil {
			t.Fatal(err)
		}
	}
}

func spentIn(t *testing.T, out string) int {
	t.Helper()
	m := regexp.MustCompile(`<u>(\d+)</u>`).FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		t.Fatalf("no spent probe in output:\n%s", mainInner(t, out))
	}
	n := 0
	for _, d := range m[len(m)-1][1] {
		n = n*10 + int(d-'0')
	}
	return n
}

// Every shape the v1.3.8 load-time rule refused now registers, compiles
// without a vet rejection, renders exactly what it renders unrestricted
// (hard-coded here), and spends a tiny part of the budget.
func TestPluginRangeFieldShapesRender(t *testing.T) { //nolint:paralleltest // reads the process-wide budget through the probe func
	shapes := []struct{ name, src, want string }{
		{"s1", `{{$svc := .Content.items}}{{range $i, $s := $svc}}[{{$i}}{{$s}}]{{end}}`, "[0a][1b][2c][3d]"},
		{"s2", `{{$n := 3}}{{range $i := $n}}{{$i}}{{end}}`, "012"},
		{"s3", `{{$core := .Content.items}}{{range $core}}{{.}}{{end}}`, "abcd"},
		{"s4", `{{range $i := len (plugin_acme_rows 4)}}{{$i}}{{end}}`, "0123"},
		{"s5", `{{$c := .Content}}{{range $c.faqs}}{{.}}{{end}}`, "xy"},
		{"s6", `{{range $i, $x := .Content.items}}{{if lt $i 3}}{{$x}}{{end}}{{end}}`, "abc"},
		{"s7", `{{range $i := 2}}{{range $j := 2}}{{$i}}{{$j}},{{end}}{{end}}`, "00,01,10,11,"},
		{"s8", `{{range $i := 5}}{{if eq $i 1}}{{continue}}{{end}}{{if eq $i 3}}{{break}}{{end}}{{$i}}{{end}}`, "02"},
	}
	partials, sections := map[string]string{}, map[string]string{}
	var host strings.Builder
	var types []string
	for _, s := range shapes {
		sections[s.name] = rangeSection(s.name, s.src)
		partials["p"+s.name] = `<b id="p` + s.name + `">` + s.src + `</b>`
		host.WriteString(`{{template "plugin/acme/p` + s.name + `" .}}`)
		types = append(types, s.name)
	}
	sections["host"] = rangeSection("host", host.String())
	sections["zprobe"] = rangeSection("zprobe", `<u>{{plugin_acme_spent}}</u>`)
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme", partials, sections)
	log, count := newWarnRecorder()
	e, err := newEngine(Options{Logger: log}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n := count("rejected") + count("skipping"); n != 0 {
		t.Fatalf("plugin templates were rejected or skipped: %d warnings", n)
	}
	out := renderToString(t, e, rangeView(append(types, "host", "zprobe")...))
	for _, s := range shapes {
		if want := `<p id="` + s.name + `">` + s.want + `</p>`; !strings.Contains(out, want) {
			t.Errorf("section %s: want %q in\n%s", s.name, want, mainInner(t, out))
		}
		if want := `<b id="p` + s.name + `">` + s.want + `</b>`; !strings.Contains(out, want) {
			t.Errorf("partial %s: want %q in\n%s", s.name, want, mainInner(t, out))
		}
	}
	if n := spentIn(t, out); n == 0 || n >= pluginRangeBudget/100 {
		t.Errorf("fixtures spent %d iterations, want 0 < n < %d", n, pluginRangeBudget/100)
	}
}

func TestPluginRangeBudgetStopsAndDegrades(t *testing.T) { //nolint:paralleltest // reads the process-wide degraded counter and warn limiter
	old := rangeWarns
	rangeWarns = &warnLimiter{last: map[string]time.Time{}}
	t.Cleanup(func() { rangeWarns = old })
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme-stops", nil, map[string]string{
		"huge":   rangeSection("huge", `{{range 1000000000}}`+mark+`{{end}}`),
		"slice":  rangeSection("slice", `{{range plugin_acme_stops_rows 1000000}}`+mark+`{{end}}`),
		"nested": rangeSection("nested", `{{$n := 100000}}{{range $i := $n}}{{range $j := $n}}`+mark+`{{end}}{{end}}`),
	})
	log, count := newWarnRecorder()
	e, err := newEngine(Options{Logger: log}, reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"huge", "slice", "nested"} {
		for i := 0; i < 2; i++ {
			before, start := DegradedCount(), time.Now()
			out := renderToString(t, e, rangeView(typ))
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("%s: render took %v", typ, d)
			}
			if n := strings.Count(out, mark); n > pluginRangeBudget || n < pluginRangeBudget-1 {
				t.Errorf("%s: %d iterations, want about %d", typ, n, pluginRangeBudget)
			}
			if DegradedCount() == before {
				t.Errorf("%s: the budget hit did not note a degraded render", typ)
			}
		}
		// Two renders, two trial renders: still one warning per plugin+template.
		if n := count("plugin=acme-stops template=section-" + typ); n != 1 {
			t.Errorf("%s: %d warnings, want 1", typ, n)
		}
	}
	if n := count("plugin template range budget exceeded"); n != 3 {
		t.Errorf("%d budget warnings, want 3", n)
	}
}

// Two partials on one page draw from one budget; the next render starts fresh.
func TestPluginRangeBudgetSharedPerRender(t *testing.T) { //nolint:paralleltest // reads the process-wide budget through the probe func
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme", map[string]string{
		"a": `{{range 60000}}` + mark + `{{end}}`,
		"b": `{{range 60000}}` + mark + `{{end}}`,
	}, map[string]string{
		"both": rangeSection("both", `{{template "plugin/acme/a" .}}{{template "plugin/acme/b" .}}`),
	})
	e, err := newEngine(Options{Logger: slog.New(slog.NewTextHandler(devNull{}, nil))}, reg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out := renderToString(t, e, rangeView("both"))
		if n := strings.Count(out, mark); n != pluginRangeBudget {
			t.Fatalf("render %d: %d items, want %d (one shared budget, fresh per render)", i, n, pluginRangeBudget)
		}
	}
}

// The trial render of the sections must not use up the page's budget.
func TestPluginRangeTrialDoesNotEatPageBudget(t *testing.T) { //nolint:paralleltest // shares the process-wide funnel state
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme", nil, map[string]string{
		"big": rangeSection("big", `{{range 80000}}`+mark+`{{end}}`),
	})
	e, err := newEngine(Options{Logger: slog.New(slog.NewTextHandler(devNull{}, nil))}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(renderToString(t, e, rangeView("big")), mark); n != 80000 {
		t.Fatalf("%d items, want all 80000", n)
	}
}

// Theme templates are not budgeted, even on a page that also has a plugin range.
func TestThemeRangeNotBudgeted(t *testing.T) { //nolint:paralleltest // shares the process-wide funnel state
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme", nil, map[string]string{
		"small": rangeSection("small", `{{range 3}}p{{end}}`),
	})
	theme := fstest.MapFS{"sections/tr.html": {Data: []byte(rangeSection("tr", `{{range 200000}}`+mark+`{{end}}`))}}
	e, err := newEngine(Options{Layers: []fs.FS{theme}, Logger: slog.New(slog.NewTextHandler(devNull{}, nil))}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(renderToString(t, e, rangeView("small", "tr")), mark); n != 200000 {
		t.Fatalf("%d theme iterations, want 200000", n)
	}
}

// No render entry point may leave a budget bound to its goroutine.
func TestRangeFunnelLeavesNothingBound(t *testing.T) { //nolint:paralleltest // reads the process-wide funnel state
	reg := newPluginRegistry()
	mustRegister(t, reg, "acme", nil, map[string]string{
		"loop": rangeSection("loop", `{{range 5}}`+mark+`{{end}}`),
	})
	e, err := newEngine(Options{Logger: slog.New(slog.NewTextHandler(devNull{}, nil))}, reg)
	if err != nil {
		t.Fatal(err)
	}
	node := &builder.Node{ID: "k1", T: "container", P: map[string]any{"tag": "section"}, C: []*builder.Node{
		{ID: "c1", T: "component", P: map[string]any{"type": "loop", "content": map[string]any{}}},
	}}
	check := func(what string) {
		t.Helper()
		if rangeBudgets.active.Load() != 0 || rangeFunnels.Load() != 0 {
			t.Fatalf("%s left state: bound=%d funnels=%d", what, rangeBudgets.active.Load(), rangeFunnels.Load())
		}
	}
	if out := renderToString(t, e, rangeView("loop")); !strings.Contains(out, strings.Repeat(mark, 5)) {
		t.Fatal("page did not render the loop")
	}
	check("RenderPage")
	v := minimalView()
	var b strings.Builder
	if err := e.RenderNode(&b, v, node, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), strings.Repeat(mark, 5)) {
		t.Fatalf("RenderNode did not render the loop: %s", b.String())
	}
	check("RenderNode")
	f, ok, err := e.RenderTop(v, node)
	if err != nil || !ok || !strings.Contains(f.HTML, strings.Repeat(mark, 5)) {
		t.Fatalf("RenderTop: ok=%v err=%v html=%s", ok, err, f.HTML)
	}
	check("RenderTop")
}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }
