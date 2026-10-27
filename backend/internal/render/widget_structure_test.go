package render

import (
	"strings"
	"testing"
)

func TestStructureWidgets(t *testing.T) {
	pub := mainInner(t, renderDoc(t, nil, loadWidgetDoc(t, "accordion"), false))
	if n := strings.Count(pub, ` name="hm-acc-a1"`); n != 2 {
		t.Errorf("items of a single accordion share a name, got %d", n)
	}
	if strings.Contains(pub, `name="hm-acc-a2"`) {
		t.Error("a non-single accordion must not set a name")
	}
	if !strings.Contains(pub, "<details") || strings.Contains(pub, "<iframe") || strings.Contains(pub, "<script") {
		t.Error("accordion is plain details")
	}
	cols := mainInner(t, renderDoc(t, nil, loadWidgetDoc(t, "columns"), false))
	for _, want := range []string{"hm-cols--2 hm-n-c2", "hm-cols--4 hm-n-c4", "hm-cols--2 hm-n-c0"} {
		if !strings.Contains(cols, want) {
			t.Errorf("columns: missing %q (a missing count falls back to 2)", want)
		}
	}
}
