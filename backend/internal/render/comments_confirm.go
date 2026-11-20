package render

import "io"

// CommentConfirmView feeds layouts/comment-confirm.html. Invalid shows the
// expired or already-used message instead of the confirm button. Action is a
// path only; it is the one place the bearer token in the URL is printed.
type CommentConfirmView struct {
	Invalid     bool
	EntityTitle string
	Action      string
}

// RenderCommentConfirm renders layouts/comment-confirm.html: the confirm
// button page or the invalid-link page. It is noindex, has no sections and
// suppresses the chrome exactly as RenderAccess does (no parts, nav, feeds or
// contact float, and no plugin, tracking or page script), so nothing on the
// page can read the token in the URL. v.CommentConfirm must be set.
func (e *Engine) RenderCommentConfirm(w io.Writer, v *PageView, status int) error {
	nv := *v
	nv.Sections, nv.Doc = nil, nil
	nv.Kind = KindPage
	nv.Page.NoIndex, nv.Page.RobotsRaw = true, ""
	nv.NoParts = true
	nv.Site.ContactFloat = ContactFloat{}
	nv.Site.Nav, nv.Site.FooterNav, nv.Site.SocialLinks = nil, nil, nil
	nv.Site.HeaderCTA, nv.Site.Feeds = nil, nil
	if nv.CommentConfirm == nil {
		nv.CommentConfirm = &CommentConfirmView{Invalid: true}
	}
	if nv.Page.Title == "" {
		nv.Page.Title = nv.Site.Name
	}
	return e.execute(w, []string{"comment-confirm"}, &nv, status)
}
