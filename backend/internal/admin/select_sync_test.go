package admin

import (
	"os"
	"regexp"
	"testing"
)

// Setting an enhanced select's value in code fires no event, so the styled trigger keeps the
// old label (a draft that looks like a draft but saves as scheduled). The blog editor must
// notify the status select right after it writes the value.
func TestBlogEditorScheduleNotifiesStatusSelect(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/blogeditor.js")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`status\.value = "scheduled";\s*(//[^\n]*\n\s*)*status\.dispatchEvent\(new Event\("change", \{ bubbles: true \}\)\)`)
	if !re.Match(b) {
		t.Error(`blogeditor.js sets status.value = "scheduled" without dispatching a bubbling change on it`)
	}
}
