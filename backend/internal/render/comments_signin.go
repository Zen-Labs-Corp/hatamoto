package render

import (
	"io"
	"strings"
)

// States of the comment sign-in page.
const (
	CommentSignInForm    = "form"    // ask for the email address
	CommentSignInSent    = "sent"    // the mail is on its way (same text for every address)
	CommentSignInIn      = "in"      // this browser is signed in: sign-out button
	CommentSignInOut     = "out"     // this browser was just signed out
	CommentSignInLink    = "link"    // the mailed link opened: sign-in button
	CommentSignInInvalid = "invalid" // the mailed link expired or was used
)

// CommentSignInView feeds layouts/comment-signin.html. State is one of the
// CommentSignIn* constants. Action is the path the state's POST form targets
// and Token the single-use form token it carries; Action is the one place a
// bearer token in the URL is printed. EmailName is the email input's name and
// Email the typed address after an error; a non-empty Error shows the
// invalid-address text.
type CommentSignInView struct {
	State, Action, Token string
	EmailName, Email     string
	Error                string
}

// RenderCommentSignIn renders layouts/comment-signin.html. Like
// RenderCommentConfirm it is noindex, has no sections and suppresses the
// chrome and every script, and the canonical link and og:url name the site
// root instead of the (possibly token) path. A nil v.CommentSignIn renders the
// invalid-link state.
func (e *Engine) RenderCommentSignIn(w io.Writer, v *PageView, status int) error {
	nv := *v
	nv.Sections, nv.Doc = nil, nil
	nv.Kind = KindPage
	nv.Page.NoIndex, nv.Page.RobotsRaw = true, ""
	nv.Page.CanonicalURL = strings.TrimRight(nv.BaseURL, "/") + "/"
	nv.NoParts = true
	nv.Site.ContactFloat = ContactFloat{}
	nv.Site.Nav, nv.Site.FooterNav, nv.Site.SocialLinks = nil, nil, nil
	nv.Site.HeaderCTA, nv.Site.Feeds = nil, nil
	if nv.CommentSignIn == nil {
		nv.CommentSignIn = &CommentSignInView{State: CommentSignInInvalid}
	}
	if nv.Page.Title == "" {
		nv.Page.Title = nv.Site.Name
	}
	return e.execute(w, []string{"comment-signin"}, &nv, status)
}
