package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Settings > General (unslop2): the page nav is the shared hm/tabs strip (Identity, Homepage;
// no Email anchor; the business details live under Locales), the Identity description links to Theme colours through an escaped
// href built in Go, and the two tabbed form cards opt in to a sticky footer. Reverting any of these
// fails here.
func TestSettingsGeneralStripAndStickySave(t *testing.T) {
	t.Parallel()

	page := readWeb(t, "pages/settings/general.html")
	for _, want := range []string{`template "hm/tabs"`, "DescriptionHTML"} {
		if !strings.Contains(page, want) {
			t.Errorf("general.html lacks %q", want)
		}
	}
	if n := strings.Count(page, "hm-card--sticky-foot"); n < 2 {
		t.Errorf("general.html: %d sticky-footer cards, want at least 2", n)
	}
	for _, bad := range []string{"hm-jumpnav", `href="#email"`, "live in Appearance"} {
		if strings.Contains(page, bad) {
			t.Errorf("general.html still has %q", bad)
		}
	}

	src, err := os.ReadFile("../general.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"colours"`, "html.EscapeString", "#identity"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("general.go lacks %q", want)
		}
	}
	if strings.Contains(string(src), "#email") {
		t.Error("general.go still has an #email tab")
	}

	rule := lineWith(adminAsset(t, "css", "partials", "card.css"), ".hm-card--sticky-foot .hm-card__foot {")
	if rule == "" {
		t.Fatal("card.css: no .hm-card--sticky-foot .hm-card__foot rule")
	}
	for _, want := range []string{"position: sticky; bottom: 0", "var(--color-paper)", "border-top"} {
		if !strings.Contains(rule, want) {
			t.Errorf("card.css: the sticky footer lacks %q", want)
		}
	}
}
