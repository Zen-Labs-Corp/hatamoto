package render

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestPluginTemplatesRefusedRegisterTime(t *testing.T) { //nolint:paralleltest // uses the process-wide plugin registry
	const plugin = "acme-refused-reg"
	t.Cleanup(func() { RemovePlugin(plugin) })
	if err := RegisterPluginSection(plugin, "Bad Type", `x`); err == nil {
		t.Fatal("bad section type accepted")
	}
	if err := RegisterPluginPartial(plugin, "p", ``); err == nil {
		t.Fatal("empty partial accepted")
	}
	got := PluginTemplatesRefused(plugin)
	if len(got) != 2 {
		t.Fatalf("refused = %q, want two entries", got)
	}
	for _, g := range got {
		if strings.HasPrefix(g, "render: ") || !strings.Contains(g, plugin) {
			t.Errorf("entry %q: want the error text without its prefix, naming the plugin", g)
		}
	}
	// The dry run records nothing.
	if err := CheckPluginPartial(plugin, "q", ``); err == nil {
		t.Fatal("dry run accepted an empty partial")
	}
	if n := len(PluginTemplatesRefused(plugin)); n != 2 {
		t.Fatalf("the dry run changed the record: %d entries", n)
	}
	RemovePlugin(plugin)
	if got := PluginTemplatesRefused(plugin); got != nil {
		t.Fatalf("RemovePlugin left %q", got)
	}
}

func TestPluginTemplatesRefusedCompileTime(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	for typ, src := range map[string]string{
		"thief":  `{{define "section-thief"}}T{{end}}{{define "head"}}H{{end}}`, // vet: redefines a theme template
		"fine":   `{{define "section-fine"}}F{{end}}`,
		"broken": `{{define "section-broken"}}{{if}}{{end}}`, // vet rejects the whole plugin
	} {
		owner := "acme-" + typ
		if err := reg.registerSection(owner, typ, src); err != nil {
			t.Fatal(err)
		}
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-thief"); len(got) != 1 || !strings.HasPrefix(got[0], "all templates: sections/thief.html: ") || !strings.Contains(got[0], `defines "head"`) {
		t.Errorf("thief: %q", got)
	}
	if got := reg.templatesRefused("acme-broken"); len(got) != 1 || !strings.HasPrefix(got[0], "all templates: ") {
		t.Errorf("broken: %q", got)
	}
	if got := reg.templatesRefused("acme-fine"); got != nil {
		t.Errorf("fine: %q", got)
	}
	// A fixed version replaces the compile-time record on the next compile.
	reg.removePlugin("acme-thief")
	if err := reg.registerSection("acme-thief", "thief", `{{define "section-thief"}}T{{end}}`); err != nil {
		t.Fatal(err)
	}
	e.current()
	if got := reg.templatesRefused("acme-thief"); got != nil {
		t.Errorf("fixed plugin still refused: %q", got)
	}
	if got := reg.templatesRefused("acme-broken"); len(got) != 1 {
		t.Errorf("broken record lost on an unrelated rebuild: %q", got)
	}
}

// A section that passes vet but fails the engine's own compile is skipped, and
// the skip is listed too.
func TestPluginTemplatesRefusedCompileSkip(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	if err := reg.registerSection("acme-skip", "esc", `{{define "section-esc"}}{{template "plugin/acme_skip/nope" .}}{{end}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := newEngine(Options{}, reg); err != nil {
		t.Fatal(err)
	}
	got := reg.templatesRefused("acme-skip")
	if len(got) != 1 || !strings.HasPrefix(got[0], "section esc: ") {
		t.Errorf("skip: %q", got)
	}
}

func TestPluginTemplatesRefusedCapped(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	for i := 0; i < 30; i++ {
		_ = reg.registerSection("acme", "Bad"+fmt.Sprint(i), "x")
		reg.noteRefused("acme", fmt.Errorf("render: refusal %02d", i))
		reg.noteRefused("acme", fmt.Errorf("render: refusal %02d", i)) // a duplicate adds nothing
	}
	got := reg.templatesRefused("acme")
	if len(got) != maxRefusedPerPlugin {
		t.Fatalf("%d entries, want %d", len(got), maxRefusedPerPlugin)
	}
	if !slices.IsSorted(got) {
		t.Errorf("not sorted: %q", got)
	}
	if reg.size() != newPluginRegistry().size() {
		t.Error("recording a refusal changed the registry change counter")
	}
}

// A call-graph fault is listed as not registered, with its reason, and is not
// reported as a script stop: the plugin has no scripts to ask site.scripts for.
func TestPluginTemplatesRefusedCallGraph(t *testing.T) { //nolint:paralleltest // uses the process-wide plugin registry
	const plugin = "acme-selfcall"
	if err := RegisterPluginPartial(plugin, "loop", `<p>{{template "plugin/acme_selfcall/loop" .}}</p>`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemovePlugin(plugin) })
	got := PluginTemplatesRefused(plugin)
	if len(got) != 1 || !strings.HasPrefix(got[0], "partial plugin/acme_selfcall/loop: ") || strings.Contains(got[0], "site.scripts") {
		t.Fatalf("refused = %v", got)
	}
	if stopped := PluginMarkupStopped(plugin); len(stopped) != 0 {
		t.Errorf("stopped = %v, want none", stopped)
	}
}

// The gate scan is memoised: repeated admin reads do not rerun it, and a
// registry change or a grant change does.
func TestPluginGateMemoised(t *testing.T) { //nolint:paralleltest // uses the process-wide plugin registry and script gate
	const plugin = "acme-memo"
	t.Cleanup(func() { SetPluginScriptGate(nil); RemovePlugin(plugin) })
	SetPluginScriptGate(nil)
	if err := RegisterPluginPartial(plugin, "card", `<p>{{.}}</p>`); err != nil {
		t.Fatal(err)
	}
	scans := func() int64 { return defaultPlugins.gateScans.Load() }
	PluginTemplatesRefused(plugin)
	base := scans()
	PluginTemplatesRefused(plugin)
	PluginMarkupStopped(plugin)
	if scans() != base {
		t.Fatalf("repeat calls reran the scan: %d -> %d", base, scans())
	}
	if err := RegisterPluginPartial(plugin, "more", `<b>{{.}}</b>`); err != nil {
		t.Fatal(err)
	}
	PluginMarkupStopped(plugin)
	if scans() != base+1 {
		t.Fatalf("registry change did not invalidate: %d, want %d", scans(), base+1)
	}
	SetPluginScriptGate(func(p string) bool { return p == plugin })
	PluginTemplatesRefused(plugin)
	if scans() != base+2 {
		t.Fatalf("grant change did not invalidate: %d, want %d", scans(), base+2)
	}
	PluginTemplatesRefused(plugin)
	if scans() != base+2 {
		t.Fatalf("repeat after grant reran the scan: %d", scans())
	}
}
