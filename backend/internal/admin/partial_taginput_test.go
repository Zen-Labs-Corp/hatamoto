package admin

import (
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialTaginput(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/taginput", "default", ui.Taginput{ID: "ti", Key: "notify_email", Label: "Recipients", Placeholder: "name@example.com", Hint: "Press Enter or comma to add."})
}
