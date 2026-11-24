package render

import (
	"html/template"
	"time"
)

// CommentThread is the public comment list of one entity, newest first. The
// server fills it only when comments are on and the page is a real view; a nil
// PageView.Comments makes the theme render nothing.
//
// Notice and Error carry a code the theme turns into text (so the words stay
// in theme.json and stay overridable). Notice: "check_email", "posted".
// Error: "body_empty", "body_long", "name", "email", "email_domain", "closed".
type CommentThread struct {
	Items              []CommentItem
	Total, Page, Pages int
	PrevURL, NextURL   string
	PageLinks          []CommentPageLink // the numbered pager; empty with one page
	Notice, Error      string
	Newest             bool
}

// CommentPageLink is one entry of the numbered pager. Page 0 is a gap ("…").
type CommentPageLink struct {
	Page    int
	URL     string
	Current bool
}

// CommentItem is one comment. Body is the stored, already sanitised body_html
// (written by the server's own formatter), so it is not passed through the
// page sanitiser, which would strip rel="ugc".
type CommentItem struct {
	ID, Author   string
	IsSiteAuthor bool
	Body         template.HTML // #nosec G203 -- sanitised on write
	CreatedAt    time.Time
	Depth        int
	Replies      []CommentItem
	ReplyURL     string // "" at the maximum depth
	Own          bool
	Editable     bool
	EditAction   string
	DeleteAction string
	Dead         bool // a dead comment: Author and Body are empty unless the viewer may see it
}

// CommentForm feeds the comments/form partial. Name, Email, Body, Honeypot and
// Token hold the rotated field names and the form token the server issues per
// request; the cached page carries CommentFormPlaceholders instead, which the
// server swaps for real values (CommentFormMarker flags such a page). The
// value fields echo typed input back after a fixable error, also as placeholders.
type CommentForm struct {
	Action   string // "/-/comments/<entity-id>"
	Token    string
	Body     string
	Name     string
	Email    string
	Honeypot string
	Extra    string // rotated name of the reply opt-in checkbox
	ParentID string
	ReplyTo  string
	Captcha  *CommentCaptcha
	MaxBody  int
	HX       bool

	// BodyValue, NameValue and EmailValue are set only when a fixable error
	// sends the form back; they hold CommentValuePlaceholders, which the server
	// replaces with the escaped typed values. Notify re-checks the reply opt-in.
	BodyValue, NameValue, EmailValue string
	Notify                           bool

	// Focus names the field an error is about ("body", "name" or "email"); the
	// default form puts autofocus on it so the browser lands at the form.
	Focus string
}

// CommentCaptcha names the captcha widget the form shows.
type CommentCaptcha struct{ Provider, SiteKey string }

// CommentFormMarker is the attribute the default form carries so the server
// can tell a rendered page holds the comment form slots.
const CommentFormMarker = "data-hm-cform"

// CommentFormPlaceholders are the values a cached page prints in the form's
// token and field-name slots before the server fills them per request.
var CommentFormPlaceholders = CommentForm{
	Token: "__hmcf_tok__", Body: "__hmcf_body__", Name: "__hmcf_name__",
	Email: "__hmcf_email__", Honeypot: "__hmcf_hp__", Extra: "__hmcf_extra__",
}

// CommentValuePlaceholders are what the value fields hold on an error render.
// The server swaps them for the HTML-escaped values in the same single pass as
// the token and names, so typed text is never rewritten.
var CommentValuePlaceholders = CommentForm{
	BodyValue: "__hmcf_vbody__", NameValue: "__hmcf_vname__", EmailValue: "__hmcf_vemail__",
}
