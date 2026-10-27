package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialSegmented(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/segmented", "default", ui.Segmented{Label: "Status", Action: "/admin/x/status", Name: "status", CSRF: "CSRF", Items: []ui.SegmentedItem{
		{Label: "New", Value: "new", Pressed: true}, {Label: "Read", Value: "read"},
	}})
	for _, want := range []string{`role="group" aria-label="Status"`, `name="status" value="new" aria-pressed="true"`, `name="status" value="read" aria-pressed="false"`, `name="_csrf"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	if _, err := a.RenderPartial("hm/segmented", ui.Segmented{Label: "Status"}); err == nil {
		t.Error("a segmented control without an Action must not render")
	}
	r := checkPartial(t, a, "hm/segmented", "radio", ui.Segmented{Mode: "radio", Label: "Access", Name: "purpose.x", Attrs: map[string]string{"data-aia-saved": "allow"}, Items: []ui.SegmentedItem{
		{Label: "Allow", Value: "allow", Pressed: true}, {Label: "Block", Value: "deny"},
	}})
	for _, want := range []string{`<fieldset class="hm-segmented" data-aia-saved="allow">`, `<legend class="hm-visually-hidden">Access</legend>`, `type="radio" name="purpose.x" value="allow" checked>`, `name="purpose.x" value="deny"><span class="hm-segmented__btn">Block`} {
		if !strings.Contains(r, want) {
			t.Errorf("radio mode lacks %q in %s", want, r)
		}
	}
	b := checkPartial(t, a, "hm/segmented", "buttons", ui.Segmented{Mode: "buttons", Label: "Preview width", Items: []ui.SegmentedItem{
		{Label: "Desktop", Pressed: true, Attrs: map[string]string{"data-hm-bs-device": "base"}}, {Label: "Phone", Attrs: map[string]string{"data-hm-bs-device": "sm"}},
	}})
	for _, want := range []string{`role="group" aria-label="Preview width"`, `type="button" class="hm-segmented__btn" aria-pressed="true" data-hm-bs-device="base"`, `aria-pressed="false" data-hm-bs-device="sm"`} {
		if !strings.Contains(b, want) {
			t.Errorf("buttons mode lacks %q in %s", want, b)
		}
	}
	if strings.Contains(b, "<form") {
		t.Error("buttons mode must not render a form")
	}
}
