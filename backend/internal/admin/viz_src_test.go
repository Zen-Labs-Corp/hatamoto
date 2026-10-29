package admin

import (
	"os"
	"strings"
	"testing"
)

func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// role=progressbar makes its children presentational: the visible value text
// ("120 of 100") must reach assistive tech as aria-valuetext, because
// aria-valuenow is clamped to the bar.
func TestProgressHasValueText(t *testing.T) {
	t.Parallel()

	if !strings.Contains(readSrc(t, "../../web/admin/partials/progress.html"), `aria-valuetext="{{.Value}}"`) {
		t.Error("hm/progress needs aria-valuetext with its visible value")
	}
}
