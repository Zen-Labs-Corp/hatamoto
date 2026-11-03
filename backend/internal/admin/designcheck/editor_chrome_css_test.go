package designcheck

import (
	"strings"
	"testing"
)

// The editors (builder, blog editor) use sentence-case captions, never
// uppercase, and the builder Layers panel marks the selected row with a solid
// accent fill and paper text (ADMIN_UI_RULES, Selected state), with no faded
// text on it and no transparent stand-in.
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
	if !strings.Contains(rule, "background: var(--bs-nav-accent)") || !strings.Contains(rule, "color: var(--bs-nav-on-accent)") {
		t.Errorf("the selected Layers row must be solid --bs-nav-accent (--color-accent) with --bs-nav-on-accent (--color-paper) text: %s", rule)
	}
	if strings.Contains(rule, "transparent") || strings.Contains(rule, "opacity") {
		t.Errorf("the selected Layers row must not use a translucent fill or faded text (paper on accent has to stay AA): %s", rule)
	}
	if !strings.Contains(css, "@media (forced-colors: active) { .hm-bs-row[aria-selected=\"true\"] { outline: 2px solid Highlight;") {
		t.Error("the selected Layers row needs its Highlight outline in forced-colors mode")
	}
	if !strings.Contains(css, "--bs-nav-band: color-mix(in srgb, var(--color-accent) ") {
		t.Error("the Layers band (--bs-nav-band) must be an accent tint (accent-soft is grey in the admin palette), lighter than the solid selected row")
	}
	if !strings.Contains(css, ".hm-bs-row--band { box-shadow: inset 2px 0 0 var(--bs-nav-accent); }") {
		t.Error("the Layers band needs its accent edge, a cue that is not colour alone")
	}

	if !strings.Contains(css, "inline-size: min(max(var(--hm-bs-insp), 22.5rem), 100%)") {
		t.Error("the .hm-bs__seo drawer width needs a 22.5rem floor: --hm-bs-insp is 0px while the inspector is hidden")
	}
}
