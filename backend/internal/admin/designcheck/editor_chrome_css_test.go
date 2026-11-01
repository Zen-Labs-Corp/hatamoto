package designcheck

import (
	"strings"
	"testing"
)

// The editors (builder, blog editor) use sentence-case captions, never
// uppercase, and the builder Navigator marks the selected row with an
// accent-soft tint plus a full accent outline and edge, not a solid fill
// and not a faint tint-only hairline.
func TestEditorsCSSRules(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"builder.css", "builderinspector.css", "blogeditor.css"} {
		if strings.Contains(adminAsset(t, "css", "pages", name), "text-transform") {
			t.Errorf("%s: text-transform is not used in the editors; captions are sentence case", name)
		}
	}

	css := adminAsset(t, "css", "pages", "builder.css")
	var rule string
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(line, `.hm-bs-row[aria-selected="true"],`) {
			rule = line
			break
		}
	}
	if rule == "" {
		t.Fatal(`builder.css: no .hm-bs-row[aria-selected="true"] rule`)
	}
	if !strings.Contains(rule, "background: var(--color-accent-soft)") {
		t.Errorf("the selected Navigator row must use --color-accent-soft: %s", rule)
	}
	if !strings.Contains(rule, "inset 0 0 0 1px var(--color-accent)") || !strings.Contains(rule, "inset 3px 0 0 var(--color-accent)") {
		t.Errorf("the selected Navigator row needs a full accent outline and a 3px accent edge (3:1 against the tint): %s", rule)
	}
	if strings.Contains(rule, "transparent") {
		t.Errorf("the selected Navigator row must not use a translucent hairline: %s", rule)
	}
	if !strings.Contains(css, "--bs-nav-band: color-mix(") {
		t.Error("the Navigator band must be lighter than the selected tint")
	}
}
