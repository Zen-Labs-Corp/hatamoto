package render

import (
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
)

// recordRig is a registry whose plugin section is refused by vet, plus a theme
// layer that defines the same section type: compiling that chain records
// nothing for the plugin, so a write to the record is visible.
func recordRig(t *testing.T) (reg *pluginRegistry, e *Engine, layer fstest.MapFS, before []string) {
	t.Helper()
	reg = newPluginRegistry()
	if err := reg.registerSection("acme-shadow", "shadow", `{{define "section-shadow"}}{{if}}{{end}}`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	before = reg.templatesRefused("acme-shadow")
	if len(before) != 1 {
		t.Fatalf("setup: refused = %q, want one entry", before)
	}
	layer = fstest.MapFS{"sections/shadow.html": {Data: []byte(`{{define "section-shadow"}}<p>t</p>{{end}}`)}}
	return reg, e, layer, before
}

func TestCheckThemeKeepsLiveRefusedRecord(t *testing.T) {
	t.Parallel()
	reg, e, layer, before := recordRig(t)
	if err := e.CheckTheme(layer); err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-shadow"); !slices.Equal(got, before) {
		t.Errorf("CheckTheme changed the record: %q, want %q", got, before)
	}
}

func TestSmokeRenderKeepsLiveRefusedRecord(t *testing.T) {
	t.Parallel()
	reg, e, layer, before := recordRig(t)
	if err := e.smokeRender(layered([]fs.FS{layer})); err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-shadow"); !slices.Equal(got, before) {
		t.Errorf("smokeRender changed the record: %q, want %q", got, before)
	}
}

func TestPreviewEngineKeepsLiveRefusedRecord(t *testing.T) {
	t.Parallel()
	reg, _, layer, before := recordRig(t)
	if _, err := newEngine(Options{Preview: true, Layers: []fs.FS{layer}}, reg); err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-shadow"); !slices.Equal(got, before) {
		t.Errorf("a preview engine changed the record: %q, want %q", got, before)
	}
}

// Proves the tests above are not vacuous: the serving compile does record.
func TestLiveCompileStillRecords(t *testing.T) {
	t.Parallel()
	reg, _, layer, _ := recordRig(t)
	if _, err := newEngine(Options{Layers: []fs.FS{layer}}, reg); err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-shadow"); got != nil {
		t.Errorf("a serving compile left the record: %q", got)
	}
}
