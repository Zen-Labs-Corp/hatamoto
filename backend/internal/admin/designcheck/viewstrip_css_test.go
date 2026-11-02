package designcheck

import (
	"os"
	"strings"
	"testing"
)

// A segmented view strip in a results card has one shared spacing rule, scoped to the
// Entities page: its own row, inset to the toolbar padding, a hairline below. No bare
// segmented rule may carry padding, or the media, plugins and taxonomies strips would be
// padded twice.
func TestViewStripHasOneSharedRule(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/css/partials/tabs.css")
	if err != nil {
		t.Fatal(err)
	}
	const scoped = `.hm-entities .hm-tabs[data-variant="segmented"] {`
	const bare = `.hm-tabs[data-variant="segmented"] {`
	var rules int
	for i, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, scoped):
			rules++
			for _, want := range []string{"var(--space-3)", "var(--space-4)", "border-bottom: 1px solid var(--border)"} {
				if !strings.Contains(line, want) {
					t.Errorf("tabs.css:%d view strip rule lacks %q", i+1, want)
				}
			}
		case strings.HasPrefix(line, bare) && strings.Contains(line, "padding"):
			t.Errorf("tabs.css:%d a bare segmented rule pads the strip everywhere: %s", i+1, line)
		}
	}
	if rules != 1 {
		t.Errorf("tabs.css has %d view strip rules, want exactly 1", rules)
	}
}
