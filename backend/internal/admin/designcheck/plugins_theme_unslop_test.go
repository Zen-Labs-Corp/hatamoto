package designcheck

import (
	"strings"
	"testing"
)

// Signs 9-14 (unslop2, Plugins and Theme): the loud leftovers stay gone. Each test names the sign and
// fails when its fix is reverted.

// cssLines returns the lines of css that start with prefix.
func cssLines(css, prefix string) []string {
	var out []string
	for _, l := range strings.Split(css, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// Sign 9: the empty Plugins list only explains; Upload and Browse stay in the header and the footer link is gone.
func TestPluginsEmptyStateHasNoButtons(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/plugins.html")
	if strings.Contains(page, "EmptyActions") {
		t.Error("sign 9: settings/plugins.html passes Actions to the empty state (the header keeps Upload and Browse)")
	}
	if strings.Contains(page, "Browse the plugin registry") {
		t.Error("sign 9: the footer link to the registry is back on the installed list")
	}
	if src := readSrc(t, "../plugins.go"); strings.Contains(src, "EmptyActions") {
		t.Error("sign 9: plugins.go builds EmptyActions again")
	}
}

// Sign 11: Install on a card is an outline pill, not a solid fill.
func TestPluginCardInstallIsOutline(t *testing.T) {
	t.Parallel()

	rules := cssLines(adminAsset(t, "css", "pages", "pluginbrowse.css"), ".hm-plugincard__action {")
	if len(rules) != 1 {
		t.Fatalf("sign 11: want one .hm-plugincard__action rule, found %d", len(rules))
	}
	if strings.Contains(rules[0], "background: var(--text)") || !strings.Contains(rules[0], "border: 1px solid var(--border)") {
		t.Errorf("sign 11: the card's Install must be an outline, not a solid fill: %s", rules[0])
	}
}

// Sign 11 and 12: browse cards carry no star pill (a muted mono line says "First-party"); browse uses the list bar.
func TestBrowseCardHasNoStarPill(t *testing.T) {
	t.Parallel()

	plugins := readWeb(t, "pages/plugins/browse.html")
	card := plugins[strings.Index(plugins, `{{define "browse-card"}}`):]
	if strings.Contains(card, `"Icon" "star"`) || strings.Contains(card, "hm/badge") {
		t.Error("sign 11: the plugin browse card has a star pill again (use the muted Meta line)")
	}
	for _, p := range []string{"pages/plugins/browse.html", "pages/theme/browse.html"} {
		page := readWeb(t, p)
		if !strings.Contains(page, `"ListBar" true`) || strings.Contains(page, `"Bare" true`) {
			t.Errorf("sign 12: %s must use the shared list bar (ListBar true, not Bare)", p)
		}
		if !strings.Contains(page, "gt .Pager.Total .Pager.PerPage") {
			t.Errorf("sign 12: %s shows the pager on a single page", p)
		}
	}
}

// Sign 13: a theme card has a hairline border, its menu in the name row and no footer band.
func TestThemeCardHasNoFooterBand(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/theme.html")
	for _, bad := range []string{"hm/card-foot", "hm-card__foot"} {
		if strings.Contains(page, bad) {
			t.Errorf("sign 13: settings/theme.html has %q: the 56px footer band is back", bad)
		}
	}
	css := adminAsset(t, "css", "pages", "packages.css")
	for _, l := range cssLines(css, ".hm-theme") {
		if strings.Contains(l, "--admin-accent") || strings.Contains(l, "--color-accent") || (strings.HasPrefix(l, ".hm-theme--active") && strings.Contains(l, "box-shadow")) {
			t.Errorf("sign 13: a theme rule paints the admin accent or a ring: %s", l)
		}
	}
}

// Sign 14: the colours page has no top callout; its notes sit under the swatches they concern.
func TestColoursPageHasNoTopCallout(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/themedesign.html")
	if strings.Contains(page, "hm/callout-start") {
		t.Error("sign 14: themedesign.html has a callout again (put the note under the two swatches)")
	}
	if n := strings.Count(page, "hm-swatchrow__note"); n != 2 {
		t.Errorf("sign 14: %d swatch notes, want 2 (Secondary and Theme accent)", n)
	}
}
