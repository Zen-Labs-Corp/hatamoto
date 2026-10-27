package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialConfirmcard(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/confirmcard", "default", ui.Confirmcard{Post: "/admin/x/delete", Back: "/admin/x", Label: "Delete x", Message: "x will be deleted.", CSRF: "tok", Danger: true,
		Hidden: []ui.ConfirmHidden{{Name: "id", Value: "7"}}})
}

// The required fields fail the render, so a confirm page cannot ship a form
// with no action, no way back or no verb on its button.
func TestPartialConfirmcardRequiresItsContract(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	full := ui.Confirmcard{Post: "/admin/x/delete", Back: "/admin/x", Label: "Delete x", Message: "m"}
	for name, c := range map[string]struct {
		mod  func(*ui.Confirmcard)
		want string
	}{
		"post":  {func(c *ui.Confirmcard) { c.Post = "" }, "Post"},
		"back":  {func(c *ui.Confirmcard) { c.Back = "" }, "Back"},
		"label": {func(c *ui.Confirmcard) { c.Label = "" }, "Label"},
	} {
		d := full
		c.mod(&d)
		if _, err := a.RenderPartial("hm/confirmcard", d); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %s, got %v", name, c.want, err)
		}
	}
	if _, err := a.RenderPartial("hm/confirmcard", full); err != nil {
		t.Errorf("a full contract must render: %v", err)
	}
}
