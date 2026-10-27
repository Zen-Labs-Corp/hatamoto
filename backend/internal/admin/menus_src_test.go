package admin

import (
	"os"
	"strings"
	"testing"
)

// menu.css gives every menuitem an enabled look and hover; the menu tree must override it for
// disabled Indent/Outdent so they do not look clickable.
func TestMenuTreeStylesDisabledMenuItems(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/css/pages/menus.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `[role="menuitem"]:disabled`) {
		t.Error("menus.css lacks a :disabled style for menu items")
	}
}
