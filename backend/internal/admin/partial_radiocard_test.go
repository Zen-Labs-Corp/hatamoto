package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialRadiocard(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/radiocard", "default", ui.RadioCard{Legend: "Theme", Name: "theme", Items: []ui.RadioCardItem{
		{Value: "default", Title: "Default", Description: "The built-in theme.", Checked: true, Tag: &ui.Badge{Label: "Active", Kind: "success"}},
		{Value: "journal", Title: "Journal", Description: "Serif, calm blog."},
	}})
	for _, want := range []string{`<legend class="hm-field__label">Theme</legend>`, `id="theme-0" name="theme" value="default" checked`, `id="theme-1" name="theme" value="journal">`, `hm-badge--success`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	s = checkPartial(t, a, "hm/radiocard", "prefixed", ui.RadioCard{Legend: "Keep", Name: "keep", IDPrefix: "keep-2", Cols: 2, Items: []ui.RadioCardItem{{Value: "a", Title: "Ann"}, {Value: "b", Title: "Bo"}}})
	if !strings.Contains(s, `hm-radiocards--2`) || !strings.Contains(s, `id="keep-2-1" name="keep"`) {
		t.Errorf("columns and id prefix: %s", s)
	}
	s = checkPartial(t, a, "hm/radiocard", "three", ui.RadioCard{Legend: "Mode", Name: "mode", Cols: 3, ID: "mode", DescribedBy: "mode", Items: []ui.RadioCardItem{{Value: "a", Title: "A"}, {Value: "b", Title: "B"}, {Value: "c", Title: "C"}}})
	if !strings.Contains(s, `hm-radiocards--3"`) || !strings.Contains(s, `id="mode" aria-describedby="mode"`) {
		t.Errorf("three columns, id and describedby: %s", s)
	}
	if _, err := a.RenderPartial("hm/radiocard", ui.RadioCard{Name: "x"}); err == nil {
		t.Error("radio cards without a legend must not render")
	}
}
