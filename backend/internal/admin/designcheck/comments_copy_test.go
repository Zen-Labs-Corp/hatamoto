package designcheck

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
)

// The templates keep the comment warnings as literals; they must stay equal to the exported copy
// the kit review screen and the CLI print.
func TestCommentsTemplatesMatchExportedCopy(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"pages/settings/comments.html", "pages/settings/comments-enable.html"} {
		src := readWeb(t, name)
		for _, want := range []string{comment.EnableWarning, comment.EnableConfirmWarning, comment.EnableTick} {
			if name == "pages/settings/comments-enable.html" && want == comment.EnableWarning {
				continue // the enable page shows the second warning and the tick only
			}
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
}

// The community policy copy on the settings page and the per-type panel stays equal to the exported copy.
func TestCommentsPolicyTemplatesMatchExportedCopy(t *testing.T) {
	t.Parallel()

	for name, wants := range map[string][]string{
		"pages/settings/comments.html":        {comment.PolicyOnWarning, comment.PolicyConfirmWarning, comment.PolicyTick},
		"pages/settings/comments-policy.html": {comment.PolicyConfirmWarning, comment.PolicyTick},
	} {
		src := readWeb(t, name)
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
}
