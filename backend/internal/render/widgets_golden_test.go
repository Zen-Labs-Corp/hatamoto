package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// widgetGoldenDir holds one doc per widget kind (<kind>.json, hand-written) and
// the markup it must render to: <kind>.public.html and <kind>.canvas.html, the
// inside of <main>. Refresh the output files with -update.
const widgetGoldenDir = "testdata/widgets"

// compareWidgetGolden compares got with testdata/widgets/<name>.<variant>.html.
func compareWidgetGolden(t *testing.T, name, variant, got string) {
	t.Helper()
	path := filepath.Join(widgetGoldenDir, name+"."+variant+".html")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if got != string(want) {
		t.Fatalf("golden %s.%s mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, variant, got, want)
	}
}

func loadWidgetDoc(t *testing.T, name string) *builder.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(widgetGoldenDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := builder.Parse(raw)
	if err != nil {
		t.Fatalf("%s.json: %v", name, err)
	}
	return d
}

// TestWidgetGoldens renders every testdata/widgets/<kind>.json publicly and in
// the canvas. Public markup has no data-hm-* attribute and no inline style;
// canvas markup carries the node markers.
func TestWidgetGoldens(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(widgetGoldenDir, "*.json"))
	if len(files) == 0 {
		t.Fatal("no widget fixtures")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "all" || name == "settings" { // these pages have their own tests
			continue
		}
		t.Run(name, func(t *testing.T) {
			d := loadWidgetDoc(t, name)
			pub := mainInner(t, renderDocView(t, widgetFixtureView(name), d, false))
			can := mainInner(t, renderDocView(t, widgetFixtureView(name), d, true))
			compareWidgetGolden(t, name, "public", pub)
			compareWidgetGolden(t, name, "canvas", can)
			if strings.Contains(pub, "data-hm-") {
				t.Error("public markup carries data-hm-*")
			}
			if strings.Contains(pub, " style=") && name != "html" {
				t.Error("public markup carries an inline style")
			}
			if !strings.Contains(can, "data-hm-node=") || !strings.Contains(can, `data-hm-t="`+name+`"`) {
				t.Error("canvas markup lacks node markers")
			}
		})
	}
}

// Every widget kind has a fixture, so every kind has a public and canvas golden.
func TestEveryWidgetKindHasGoldens(t *testing.T) {
	for _, k := range builder.Kinds() {
		if k.Group == "" {
			continue
		}
		for _, ext := range []string{"json", "public.html", "canvas.html"} {
			if _, err := os.Stat(filepath.Join(widgetGoldenDir, k.Kind+"."+ext)); err != nil {
				t.Errorf("%s: %v", k.Kind, err)
			}
		}
	}
}
