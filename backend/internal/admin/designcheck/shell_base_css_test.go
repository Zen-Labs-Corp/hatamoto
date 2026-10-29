package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Dismissing the last banner leaves an empty .hm-notices behind (script removes only the callout).
// The strip must collapse and the page's tighter top padding must only apply while it shows one.
func TestNoticesStripCollapsesWhenEmpty(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if !strings.Contains(css, ".hm-notices:not(:has(.hm-callout)) { display: none; }") {
		t.Error("an .hm-notices with no callout must be display: none")
	}
	if !strings.Contains(css, ".hm-notices:has(.hm-callout) + .hm-page { padding-top: var(--space-4); }") || strings.Contains(css, ".hm-notices + .hm-page") {
		t.Error("the reduced page padding must depend on the strip holding a callout")
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

// A stored "collapsed" sidebar must not hide the phone top bar (the toggle is hidden there).
func TestCollapsedSidebarOnlyOnDesktop(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, `.hm-shell[data-sidebar="collapsed"] > .hm-sidebar { display: none; }`)
	if i < 0 || !strings.Contains(css[max(0, i-200):i], "@media (min-width: 769px)") {
		t.Error("the collapsed sidebar rule must sit inside @media (min-width: 769px)")
	}
}
