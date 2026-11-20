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

// heldWhy says why a comment is waiting: "Held: contains a link, first
// comment · score 4". It is empty for an approved comment.
func heldWhy(c CommentRow) string {
	if c.Status == "approved" {
		return ""
	}
	labels := make([]string, 0, len(c.Reasons))
	for _, r := range c.Reasons {
		if l, ok := commentReasonLabels[r]; ok {
			r = l
		}
		labels = append(labels, r)
	}
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
