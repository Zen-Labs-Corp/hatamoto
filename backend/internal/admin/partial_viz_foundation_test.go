package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialProgressThinInk(t *testing.T) {
	t.Parallel()

	thin := renderChips(t, "hm/progress", ui.Progress{Label: "Cache", Value: "40%", Pct: 40, Kind: "ink", Thin: true})
	for _, want := range []string{`class="hm-meter hm-meter--thin"`, `hm-meter__fill hm-viz--ink`} {
		if !strings.Contains(thin, want) {
			t.Errorf("missing %q in %s", want, thin)
		}
	}
	plain := renderChips(t, "hm/progress", ui.Progress{Label: "Cache", Value: "40%", Pct: 40})
	if strings.Contains(plain, "thin") || !strings.Contains(plain, `class="hm-meter" role`) {
		t.Errorf("the default meter must not change: %s", plain)
	}
}

func TestPartialStatSmall(t *testing.T) {
	t.Parallel()

	small := renderChips(t, "hm/stat", map[string]any{"Variant": "ticker", "Stat": ui.Stat{Label: "Last preload", Value: "2 min ago", Small: true}})
	if !strings.Contains(small, `hm-statcard__value hm-statcard__value--sm`) {
		t.Errorf("small value class missing: %s", small)
	}
	normal := renderChips(t, "hm/stat", map[string]any{"Variant": "ticker", "Stat": ui.Stat{Label: "Pages", Value: "12"}})
	if strings.Contains(normal, "--sm") {
		t.Errorf("a default stat is not small: %s", normal)
	}
}

func TestPartialCellbarInk(t *testing.T) {
	t.Parallel()

	out, err := newPartialAdmin(t).RenderPartial("hm/cellbar", ui.Bar{Label: "Priority 3 of 5", Kind: "ink", Pct: 60})
	if err != nil || !strings.Contains(string(out), `hm-cellbar__fill hm-viz--ink`) {
		t.Errorf("ink cellbar: %v %s", err, out)
	}
}
