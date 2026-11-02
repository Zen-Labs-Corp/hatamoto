package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Dismissing the last banner leaves an empty .hm-notices behind (script removes only the callout).
// The strip must collapse so it adds no gap to the page column.
func TestNoticesStripCollapsesWhenEmpty(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), ".hm-notices:not(:has(.hm-callout)) { display: none; }") {
		t.Error("an .hm-notices with no callout must be display: none")
	}
}

// The notices strip is the first child of .hm-page, so it spans the page content column on
// every width variant (and beside the subnav) with no padding or max-width of its own.
func TestNoticesAlignWithContentColumn(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, ".hm-notices") && (strings.Contains(line, "padding") || strings.Contains(line, "max-width")) {
			t.Errorf("the notices strip takes its width from .hm-page, not %q", line)
		}
	}
	sh, err := os.ReadFile("../../../web/admin/layouts/shell.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(sh)
	page := strings.Index(html, `<div class="hm-page"`)
	notices := strings.Index(html, `<div class="hm-notices">`)
	if page < 0 || notices < page || notices > strings.Index(html, `{{template "main" .}}`) {
		t.Error("shell.html must render .hm-notices inside .hm-page, before the page content")
	}
}

// The command fields (forgot with email off, setup without a code) sit in .hm-copyrow, the
// secretbanner row, so the whole command is visible and Copy lines up with the input.
func TestCommandFieldsUseCopyRow(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".hm-copyrow > .hm-field { flex: 1 1 280px; min-width: 0; }") {
		t.Error(".hm-copyrow must let the field take the free width")
	}
	for _, page := range []string{"auth/forgot.html", "setup/account.html"} {
		b, err := os.ReadFile("../../../web/admin/pages/" + page)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `<div class="hm-copyrow">`) {
			t.Errorf("%s: the command and its Copy button belong in .hm-copyrow", page)
		}
	}
}

// .hm-card__body is a column flexbox: without align-self the segmented track stretches across the card.
func TestSegmentedTrackDoesNotStretch(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/segmented.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "display: inline-flex; align-self: flex-start;") {
		t.Error(".hm-segmented must be inline-flex with align-self: flex-start")
	}
}

// A stored "collapsed" sidebar must not hide the phone top bar (the toggle is hidden there):
// the collapse rules sit inside @media (min-width: 769px) and hide the sidebar outright, no icon rail.
func TestCollapsedSidebarOnlyOnDesktop(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, rule := range []string{
		`.hm-shell[data-sidebar="collapsed"] { grid-template-columns: minmax(0, 1fr); }`,
		`.hm-shell[data-sidebar="collapsed"] > .hm-sidebar { display: none; }`,
	} {
		i := strings.Index(css, rule)
		if i < 0 || !strings.Contains(css[max(0, i-400):i], "@media (min-width: 769px)") {
			t.Errorf("base.css: %q must sit inside @media (min-width: 769px)", rule)
		}
	}
	if strings.Contains(css, "--sidebar-rail-w") {
		t.Error("base.css: the collapsed sidebar is hidden, not shrunk to a rail")
	}
}
