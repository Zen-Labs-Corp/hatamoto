package admin

import (
	"fmt"
	"strings"
)

// commentReasonLabels maps the reason strings the comment model stores to
// the words the moderation screen shows. Plugin checker reasons arrive as free
// text and are shown verbatim.
var commentReasonLabels = map[string]string{
	"contains a link":   "contains a link",
	"new commenter":     "first comment",
	"surge mode":        "surge mode",
	"spam score":        "high spam score",
	"blocked commenter": "blocked commenter",
	"check failed":      "plugin check failed",
	"check timed out":   "plugin check timed out",
}

// HeldReasonLabels maps stored hold reasons to the words moderators see
// ("new commenter" -> "first comment"); unknown reasons pass through.
func HeldReasonLabels(reasons []string) []string {
	labels := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if l, ok := commentReasonLabels[r]; ok {
			r = l
		}
		labels = append(labels, r)
	}
	return labels
}

// heldWhy says why a comment is waiting: "Held: contains a link, first
// comment · score 4". It is empty for an approved comment.
func heldWhy(c CommentRow) string {
	if c.Status == "approved" {
		return ""
	}
	labels := HeldReasonLabels(c.Reasons)
	if len(labels) == 0 && c.Score <= 0 {
		return ""
	}
	out := "Held"
	if len(labels) > 0 {
		out += ": " + strings.Join(labels, ", ")
	}
	if c.Score > 0 {
		out += fmt.Sprintf(" · score %d", c.Score)
	}
	return out
}
