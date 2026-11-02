package designcheck

import (
	"os"
	"strings"
	"testing"
)

// The menu carries no accent colour and no ::before indicator: hover is a soft
// grey fill, active a faint chip with ink text. Covers the primary sidebar and
// the settings and plugin subnav (.hm-subnav*).
func TestNavHasNoAccent(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"sidebar.css", "settings.css"} {
		b, err := os.ReadFile("../../../web/admin/css/partials/" + f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			nav := strings.Contains(line, ".hm-nav-link") || strings.Contains(line, ".hm-subnav")
			if nav && (strings.Contains(line, "::before") || strings.Contains(line, "color-accent")) {
				t.Errorf("%s:%d nav rule uses accent or ::before: %s", f, i+1, line)
			}
		}
	}
}

// The level-2 tab row sits directly under level 1: the page gap is pulled back and the
// row has no hairline of its own (it abuts the level-1 one).
func TestSubTabsAbutLevelOne(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/tabs.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{`.hm-tabs + .hm-tabs[data-variant="sub"]`, `.hm-section-head + .hm-tabs[data-variant="sub"]`, "margin-top: calc(var(--space-1) - var(--space-6))"} {
		if !strings.Contains(css, want) {
			t.Errorf("tabs.css lost the tight level-2 spacing: missing %q", want)
		}
	}
}

// The current-entry marker colour and the tabular counts belong to the Entities
// subnav; the shared .hm-subnav__marker rule (Plugins uses it) stays as it was.
func TestSubnavMarkerStylesAreEntitiesOnly(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, ".hm-subnav__marker") {
			continue
		}
		if strings.Contains(line, "tabular-nums") || strings.Contains(line, "aria-current") {
			if !strings.HasPrefix(line, ".hm-subnav--entities ") {
				t.Errorf("settings.css:%d marker style leaks to every subnav: %s", i+1, line)
			}
		}
	}
}

// The mobile bottom-bar underline must outrank the desktop
// `.hm-shell:has(.hm-subnav) .hm-nav-link[aria-current="page"]` rule (0,4,0),
// or pages with a subnav lose their active marker on mobile.
func TestMobileNavActiveOutranksSubnavRule(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/sidebar.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "  ") && strings.Contains(line, "box-shadow: inset 0 -2px") {
			if !strings.Contains(line, ".hm-sidebar > .hm-nav-groups") || !strings.Contains(line, `> li > .hm-nav-link[aria-current="page"]`) {
				t.Errorf("mobile active rule too weak against the :has(.hm-subnav) rule: %s", line)
			}
			return
		}
	}
	t.Error("mobile aria-current rule not found in sidebar.css")
}

// The primary rail shows the current-area pill on every area; a subnav next to it must
// not hide it.
func TestSidebarCurrentIsNotSuppressedBySubnav(t *testing.T) {
	t.Parallel()

	css := partialCSS(t, "sidebar.css")
	if strings.Contains(css, ":has(.hm-subnav) .hm-nav-link") {
		t.Error("sidebar.css hides the current area pill when a subnav exists; the primary rail must show it on every area")
	}
}

// The sidebar sits on --raised, which --surface-hover equals in both themes, so its hover
// fills (links, profile, ghost foot buttons) use --sidebar-hover and never the token or kasumi.
func TestSidebarHoverIsVisible(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/sidebar.css")
	if err != nil {
		t.Fatal(err)
	}
	hovers := 0
	for i, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, ":hover") && !strings.Contains(line, ":active") {
			continue
		}
		if strings.Contains(line, "background") {
			hovers++
			if !strings.Contains(line, "--sidebar-hover") {
				t.Errorf("sidebar.css:%d hover fill must use --sidebar-hover (--surface-hover equals the sidebar background): %s", i+1, line)
			}
		}
	}
	if hovers < 3 {
		t.Errorf("found %d sidebar hover fills, want link, profile and ghost buttons", hovers)
	}
}

// On phones the nav items are icon over label; a visible count would be a third line, so
// the mobile block must clip it (visually hidden, not display:none: it stays in the name).
func TestPhoneNavBadgeIsVisuallyHidden(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/sidebar.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, "@media (max-width: 768px)")
	if i < 0 {
		t.Fatal("no phone block in sidebar.css")
	}
	phone := css[i:]
	j := strings.Index(phone, ".hm-nav-link__badge {")
	if j < 0 {
		t.Fatal("the phone block does not style .hm-nav-link__badge")
	}
	rule := phone[j : j+strings.Index(phone[j:], "}")]
	if !strings.Contains(rule, "clip: rect(0 0 0 0)") || strings.Contains(rule, "display: none") {
		t.Errorf("phone badge rule must visually hide, not display:none: %s", rule)
	}
}
