package admin

import (
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestPartialDropzone(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/dropzone", "default", ui.Dropzone{ID: "sample-file", Accept: "image/png", MaxMB: 10, Help: "PNG, up to 10 MB each."})
}
