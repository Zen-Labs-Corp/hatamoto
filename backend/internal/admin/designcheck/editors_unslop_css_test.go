package designcheck

import (
	"strings"
	"testing"
)

// The editors (builder, blog editor) use sentence-case captions, never
// uppercase, and the builder Navigator marks the selected row with an
// accent-soft tint, not a solid accent fill.
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
}
