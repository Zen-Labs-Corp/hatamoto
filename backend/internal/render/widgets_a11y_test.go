package render

import (
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// Every widget's public golden passes the widget lint with no errors.
func TestWidgetPublicGoldensAccessible(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(widgetGoldenDir, "*.json"))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "all" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			pub := mainInner(t, renderDoc(t, nil, loadWidgetDoc(t, name), false))
			errs, warns := a11y.CheckPublicWidgets(pub)
			for _, e := range errs {
				t.Error(e)
			}
			for _, w := range warns {
				t.Log("warning:", w)
			}
		})
	}
}
