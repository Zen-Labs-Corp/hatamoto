package admin

import (
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialSavestate(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	for _, st := range []string{"saved", "unsaved", "saving", "error"} {
		checkPartial(t, a, "hm/savestate", st, ui.Savestate{State: st, Text: "Text " + st})
	}
}
