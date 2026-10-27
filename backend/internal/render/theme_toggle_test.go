package render

import (
	"strings"
	"testing"
)

// The header's theme toggle is [hidden] until hm.js wires it up. Taking it out of layout (display:none) made the
// nav jump sideways when it appeared (layout shift on every page); it keeps its box and is only invisible.
func TestThemeToggleKeepsItsBoxWhileHidden(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	css, _, _ := buildBundle(e.current().theme)
	rule := ".hm-theme-toggle[hidden]{"
	i := strings.Index(strings.Join(strings.Fields(string(css)), ""), strings.ReplaceAll(rule, " ", ""))
	if i < 0 {
		t.Fatal("no .hm-theme-toggle[hidden] rule in the theme CSS")
	}
	flat := strings.Join(strings.Fields(string(css)), "")
	body := flat[i : i+strings.Index(flat[i:], "}")]
	if strings.Contains(body, "display:none") || !strings.Contains(body, "visibility:hidden") {
		t.Errorf("hidden toggle rule = %s: must stay in layout and be visibility:hidden", body)
	}
}
