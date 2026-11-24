package server

import (
	"bytes"
	"context"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
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
	// verify checks the hm_c signature and expiry only (no database): the slot
	// shows the signed-in state of a locked form from it. names memoises the
	// "Posting as" name. Both nil: every locked form reads as signed out.
	verify func(val string, now time.Time) (identity.DeviceClaims, bool)
	names  *signedNames
}

// off reports whether the comments site switch is known to be off. A nil slot
// (comments not wired) says false: callers have their own nil checks.
func (c *commentSlot) off() bool { return c != nil && c.on != nil && !c.on() }

// commentEcho is a fixable comment error sent back with what the sender
// typed. It travels on the request context, only on the POST response that
// re-renders the form; it is never put in a URL or a cookie.
type commentEcho struct {
	EntityID uuid.UUID
	Code     string // commentsubmit.Code*
	// Message is a plugin's public refusal text for CodeDraft. Like Typed it
	// lives on the POST response's context only; it is never put in a URL.
	Message  string
	ParentID string // "" or a usable parent id
	Typed    *commentsubmit.Typed
}

// commentFocus is the form field a fixable error code is about.
func commentFocus(code string) string {
	switch code {
	case commentsubmit.CodeName:
		return "name"
	case commentsubmit.CodeEmail, commentsubmit.CodeEmailDomain:
		return "email"
	}
	return "body"
}

type commentEchoKey struct{}

func withCommentEcho(ctx context.Context, e commentEcho) context.Context {
	return context.WithValue(ctx, commentEchoKey{}, &e)
}

func commentEchoFrom(ctx context.Context) *commentEcho {
	e, _ := ctx.Value(commentEchoKey{}).(*commentEcho)
	return e
}

// textareaValue escapes s for a <textarea> body. The parser drops one leading
// newline there, so a value that starts with one gets an extra.
func textareaValue(s string) string {
	e := html.EscapeString(s)
	if strings.HasPrefix(s, "\n") || strings.HasPrefix(s, "\r") {
		return "\n" + e
	}
	return e
}

// fill returns the body to write and whether the response must be private
// (no validators, no shared cache). The marker alone decides, never the site
// switch: a page that carries it is never sent with placeholders or public
// headers. A marker the slot cannot fill (an unreadable form) is returned as is
// and private. Without the marker the body is unchanged and the flag false. The
// input is never modified: it is the cached entry. The typed values of a
// context echo for this entity are inserted in the same single replacer pass as
// the token and names (never before it, never in a second pass), so typed text
// is never rewritten, whatever it contains.
func (c *commentSlot) fill(r *http.Request, body []byte) ([]byte, bool) {
	if c == nil || !bytes.Contains(body, []byte(render.CommentFormMarker)) {
		return body, false
	}
	start, end := commentFormSpan(body)
	if start < 0 {
		return body, true
	}
	span := body[start:end]
	i := bytes.Index(span, []byte(commentActionPrefix))
	if i < 0 {
		return body, true
	}
	rest := span[i+len(commentActionPrefix):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return body, true
	}
	id, err := uuid.Parse(string(rest[:j]))
	if err != nil {
		return body, true
	}
	_, netHash := c.hashIP(middleware.GetClientIP(r))
	tok := spam.IssueCommentToken(c.tokenKey, spam.CommentSubject(id.String()), netHash, c.now())
	names := spam.FieldNames(c.nameKey, tok)
	var vb, vn, ve string // empty without an echo: a value placeholder never leaks
	if e := commentEchoFrom(r.Context()); e != nil && e.EntityID == id && e.Typed != nil {
		vb, vn, ve = textareaValue(e.Typed.Body), html.EscapeString(e.Typed.Name), html.EscapeString(e.Typed.Email)
	}
	p, v, l := render.CommentFormPlaceholders, render.CommentValuePlaceholders, render.CommentLockedPlaceholders
	named, signedIn, signedOut, bodyOff, who := c.lockedState(r, span, id)
	filled := strings.NewReplacer(
		p.Token, tok, p.Body, names.Body, p.Name, names.Name,
		p.Email, names.Email, p.Honeypot, names.Honeypot, p.Extra, names.Extra,
		v.BodyValue, vb, v.NameValue, vn, v.EmailValue, ve,
		l.Named, named, l.SignedIn, signedIn, l.SignedOut, signedOut, l.BodyOff, bodyOff, l.Who, who,
	).Replace(string(span))
	out := make([]byte, 0, len(body)-len(span)+len(filled))
	out = append(out, body[:start]...)
	out = append(out, filled...)
	out = append(out, body[end:]...)
	return out, true
}

// lockedState decides the three state lines and the body switch of a locked
// form (render.CommentLockedPlaceholders): a signed-in visitor sees "Posting
// as <name>" or, with no name on record, "Signed in on this browser"; anyone
// else sees the sign-in line and a disabled body. The signature alone decides
// (a revoked device sees the signed-in form and gets sign_in on its post). The
// name is looked up only for a signed-in visitor on a locked span, and who is
// already HTML-escaped. An unlocked span needs none of it and costs nothing.
func (c *commentSlot) lockedState(r *http.Request, span []byte, entity uuid.UUID) (named, signedIn, signedOut, bodyOff, who string) {
	if !bytes.Contains(span, []byte(render.CommentLockedPlaceholders.Who)) {
		return
	}
	const hidden = "hidden"
	claims, ok := c.signedClaims(r)
	if !ok {
		return hidden, hidden, "", "disabled", ""
	}
	signedOut = hidden
	if name := c.names.get(r.Context(), claims.CommenterID, entity); name != "" {
		return "", hidden, signedOut, "", html.EscapeString(name)
	}
	return hidden, "", signedOut, "", ""
}

// signedClaims verifies the request's hm_c cookie by signature only.
func (c *commentSlot) signedClaims(r *http.Request) (identity.DeviceClaims, bool) {
	if c.verify == nil {
		return identity.DeviceClaims{}, false
	}
	ck, err := r.Cookie(identity.CookieName)
	if err != nil {
		return identity.DeviceClaims{}, false
	}
	return c.verify(ck.Value, c.now())
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
