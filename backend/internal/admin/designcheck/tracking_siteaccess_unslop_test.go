package designcheck

import (
	"strings"
	"testing"
)

// Settings > Tracking (tag directory): cards carry no category tile or label (the group heading
// says it), the ID sits in mono under the name, the footer holds only actions, the shared card
// links Cookie consent, an on tag carries a green On badge under its name, and its switch label is positive, and the per-tag page uses the compact
// width so the ID field is not page-wide.
func TestTrackingDirectoryQuietCards(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/tracking.html")
	for _, bad := range []string{"hm-plugintile", "{{.Category}}", "{{icon .Icon}}", "Do not load", "(Settings > Privacy"} {
		if strings.Contains(page, bad) {
			t.Errorf("tracking.html still has %q", bad)
		}
	}
	// A tag that is on shows a green On badge beside its ID, one that is set up but off a grey Off badge.
	for _, want := range []string{"hm-tagcard__meta", `"DescriptionHTML" .AllNote`, "Skip tags for signed-in users",
		`{{if .Enabled}}{{template "hm/badge" dict "Label" "On" "Kind" "success"}}`, `{{template "hm/badge" dict "Label" "Off" "Kind" "muted"}}`} {
		if !strings.Contains(page, want) {
			t.Errorf("tracking.html lacks %q", want)
		}
	}
	if foot := page[strings.Index(page, `class="hm-plugincard__foot"`):]; strings.Contains(foot[:strings.Index(foot, "</div>")], "hm-tagcard__id") {
		t.Error("tracking.html: the tag ID is back in the card footer")
	}

	src := readSrc(t, "../tracking_settings.go")
	for _, want := range []string{`view.Width = "compact"`, `"uet": "marketing", "linkedin": "marketing"`, "st.Enabled = stored.Vendors[v.ID].Enabled", "template.HTMLEscapeString"} {
		if !strings.Contains(src, want) {
			t.Errorf("tracking_settings.go lacks %q", want)
		}
	}
}

// Settings > Site access: one short line under the title, the technical detail under the mode
// choices, and the fields grouped under subtitles (the page visitors see, maintenance, password),
// every one still visible.
func TestSiteAccessGroupedFields(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/site-access.html")
	order := []string{`"Legend" "Mode"`, `id="mode-help"`, ">The page visitors see<", `"ID" "heading"`, `"ID" "message"`, ">Maintenance mode<", `"ID" "retry-after"`, ">Password mode<", `"ID" "password"`}
	at := -1
	for _, want := range order {
		i := strings.Index(page, want)
		if i < 0 {
			t.Errorf("site-access.html lacks %q", want)
			continue
		}
		if i < at {
			t.Errorf("site-access.html: %q is out of order", want)
		}
		at = i
	}
	if n := strings.Count(page, `class="hm-card__subtitle"`); n != 3 {
		t.Errorf("site-access.html: %d subtitles, want 3", n)
	}
	if strings.Contains(page, "data-hm-show") || strings.Contains(page, "hidden") {
		t.Error("site-access.html: a mode field is hidden; every field stays visible")
	}
}

// Leftovers of the earlier pass: the stats notice's buttons end-align and its icon tops the
// first line on a phone; the plugin browse total shows once (the pager drops its own).
func TestUnslopLeftovers(t *testing.T) {
	t.Parallel()

	viz := adminAsset(t, "css", "partials", "viz.css")
	for _, want := range []string{
		".hm-notices form { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); justify-content: flex-end; }",
		"@media (max-width: 640px) { .hm-page > .hm-notices .hm-callout { align-items: flex-start;",
	} {
		if !strings.Contains(viz, want) {
			t.Errorf("viz.css lacks %q", want)
		}
	}
	browse := adminAsset(t, "css", "pages", "pluginbrowse.css")
	if !strings.Contains(browse, ".hm-pluginbrowse__results .hm-pagination__summary { display: none; }") {
		t.Error("pluginbrowse.css: the pager repeats the plugin total")
	}
}
