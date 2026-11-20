package server

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
)

// commentActionPrefix is the start of the comment form action; the entity id follows.
const commentActionPrefix = `action="/-/comments/`

// commentSlot fills the comment form placeholders of a page body with a fresh
// per-view token and its rotated names. Only the span from the form tag carrying
// render.CommentFormMarker to the next "</form>" is rewritten.
type commentSlot struct {
	tokenKey, nameKey []byte
	hashIP            func(ip string) (ipHash, netHash string) // comment.Keys.HashIP
	now               func() time.Time
	on                func() bool
}

// fill returns the body to write and whether it was personalised. It returns
// the body unchanged when comments are off or the marker is absent. The input
// is never modified: it is the cached entry.
func (c *commentSlot) fill(r *http.Request, body []byte) ([]byte, bool) {
	if c == nil || bytes.Index(body, []byte(render.CommentFormMarker)) < 0 {
		return body, false
	}
	if c.on != nil && !c.on() {
		return body, false
	}
	start, end := commentFormSpan(body)
	if start < 0 {
		return body, false
	}
	span := body[start:end]
	i := bytes.Index(span, []byte(commentActionPrefix))
	if i < 0 {
		return body, false
	}
	rest := span[i+len(commentActionPrefix):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return body, false
	}
	id, err := uuid.Parse(string(rest[:j]))
	if err != nil {
		return body, false
	}
	ipHash, _ := c.hashIP(middleware.GetClientIP(r))
	tok := spam.IssueCommentToken(c.tokenKey, spam.CommentSubject(id.String()), ipHash, c.now())
	names := spam.FieldNames(c.nameKey, tok)
	p := render.CommentFormPlaceholders
	filled := strings.NewReplacer(
		p.Token, tok, p.Body, names.Body, p.Name, names.Name,
		p.Email, names.Email, p.Honeypot, names.Honeypot,
	).Replace(string(span))
	out := make([]byte, 0, len(body)-len(span)+len(filled))
	out = append(out, body[:start]...)
	out = append(out, filled...)
	out = append(out, body[end:]...)
	return out, true
}

// commentFormSpan returns the byte range of the first real <form> tag that
// carries the marker, through its closing "</form>"; -1, -1 when there is none.
// Escaped text (a comment body showing the marker) never starts with "<form".
func commentFormSpan(body []byte) (start, end int) {
	marker := []byte(render.CommentFormMarker)
	for off := 0; ; {
		i := bytes.Index(body[off:], []byte("<form"))
		if i < 0 {
			return -1, -1
		}
		start = off + i
		gt := bytes.IndexByte(body[start:], '>')
		if gt < 0 {
			return -1, -1
		}
		if bytes.Contains(body[start:start+gt], marker) {
			k := bytes.Index(body[start:], []byte("</form>"))
			if k < 0 {
				return -1, -1
			}
			return start, start + k + len("</form>")
		}
		off = start + gt
	}
}
