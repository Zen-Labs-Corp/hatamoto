package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialPageheaderBadge(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/pageheader", "badge", map[string]any{"Title": "Import", "Description": "Copy a site.", "Badge": ui.Badge{Label: "Scanning", Kind: "info"}})
	if !strings.Contains(s, `<div class="hm-pageheader__title"><h1>Import</h1><span class="hm-badge hm-badge--info">`) {
		t.Errorf("badge must follow the title: %s", s)
	}
	s = checkPartial(t, a, "hm/pageheader", "plain", map[string]any{"Title": "Import"})
	if strings.Contains(s, "hm-badge") || strings.Contains(s, "<p") {
		t.Errorf("no badge and no description row expected: %s", s)
	}
}
