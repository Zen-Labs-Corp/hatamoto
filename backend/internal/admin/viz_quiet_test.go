package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestQuietBarList(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/barlist", "quiet", ui.BarList{
		Label: "Pages by fetches", Quiet: true,
		Items: []ui.BarItem{{Label: "/docs", Value: "20", N: 20}, {Label: "/blog", Value: "10", N: 10}},
	})
	if !strings.Contains(s, `class="hm-barlist hm-barlist--quiet"`) {
		t.Error("hm/barlist Quiet: missing hm-barlist--quiet")
	}
}
