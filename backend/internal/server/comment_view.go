package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/render"
)

// commentReader is the slice of *comment.Service the view needs (tests use a fake).
type commentReader interface {
	Settings(context.Context) (comment.Settings, error)
	EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error)
	Thread(context.Context, uuid.UUID, int) (comment.ThreadPage, error)
	Counts(context.Context, []uuid.UUID) (map[uuid.UUID]comment.EntityCounts, error)
	ViewerComments(context.Context, uuid.UUID, uuid.UUID) ([]comment.ThreadComment, error)
	EditableComments(context.Context, uuid.UUID, uuid.UUID) ([]comment.ThreadComment, error)
}

// commentView holds what fillComments reads. device resolves a hm_c value to a
// commenter (identity.Service.Device checks the token version); nil = no own items.
type commentView struct {
	reader  commentReader
	device  func(ctx context.Context, val string) (uuid.UUID, bool)
	captcha func(ctx context.Context) *captchaWidget // nil = no widget
	policy  commentPolicyFn                          // nil = no schema has the community policy
	now     func() time.Time
	logf    func(ctx context.Context, msg string, err error)
	// showDead reports whether the viewer may read dead comments of the content
	// type's owner plugin; nil = never. A true answer makes the render private.
	showDead func(ctx context.Context, plugin string, commenterID uuid.UUID) bool
}

// commentPolicyFn is comment.Service.PolicyInfo.
type commentPolicyFn func(context.Context, uuid.UUID) (comment.PolicyInfo, error)

// lockCommentForm turns form into the locked variant (no name or email input, a
// sign-in line) when the entity's schema has the sign-in policy. A nil policy
// means no schema has it. A read error fails closed: the locked form, so a
// failed lookup never offers the free name field of a policy schema.
func lockCommentForm(ctx context.Context, policy commentPolicyFn, id uuid.UUID, form *render.CommentForm, logf func(context.Context, string, error)) {
	if policy == nil {
		return
	}
	info, err := policy(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		return
	}
	if err != nil {
		if logf != nil {
			logf(ctx, "comments: policy", err)
		}
		info.Enabled = true
	}
	if info.Enabled {
		form.Locked, form.SignInURL = true, commentSignInPath
	}
}

// Codes the theme turns into text; anything else in the query is ignored.
var (
	commentNotices = map[string]bool{"check_email": true, "posted": true}
	commentErrors  = map[string]bool{
		commentsubmit.CodeBodyEmpty: true, commentsubmit.CodeBodyLong: true, commentsubmit.CodeName: true,
		commentsubmit.CodeEmail: true, commentsubmit.CodeEmailDomain: true, commentsubmit.CodeSignIn: true,
		commentsubmit.CodeDraft: true, "closed": true,
	}
)

// fillComments sets the comment parts of v for a real entity view; see commentView.fill.
func (s *Server) fillComments(ctx context.Context, r *http.Request, rr *routeResult, v *render.PageView) {
	if s.comments == nil {
		return
	}
	cv := commentView{reader: s.comments, now: time.Now, captcha: s.captchaWidgetFor, policy: s.comments.PolicyInfo}
	if s.commentIdentity != nil {
		cv.device = s.commentIdentity.Device
	}
	cv.showDead = func(ctx context.Context, plugin string, cid uuid.UUID) bool {
		return viewerShowsDead(ctx, s.comments, s.pluginMayModerate, s.cfg.Secret, plugin, cid)
	}
	cv.logf = func(ctx context.Context, msg string, err error) {
		if s.logger != nil {
			s.logger.WarnContext(ctx, msg, "error", err)
		}
	}
	cv.fill(ctx, r, v)
}

// fill sets v.Comments, the count, the form and the own items. Comments off, a
// hidden entity, a preview or a page without an entity leave v.Comments nil.
// A service error leaves it nil too and keeps the page out of the shared cache.
func (c *commentView) fill(ctx context.Context, r *http.Request, v *render.PageView) {
	id, err := uuid.Parse(v.EntityID)
	if v.Preview || err != nil {
		return
	}
	fail := func(msg string, err error) {
		c.logf(ctx, msg, err)
		v.Comments, v.CommentForm, v.CommentCount, v.CommentsOpen = nil, nil, 0, false
		if v.Uncacheable != nil {
			*v.Uncacheable = true
		}
	}
	cfg, err := c.reader.EntityConfig(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		return
	}
	if err != nil {
		fail("comments: entity mode", err)
		return
	}
	mode := cfg.Mode
	if mode != comment.ModeOpen && mode != comment.ModeClosed {
		return
	}
	set, err := c.reader.Settings(ctx)
	if err != nil {
		fail("comments: settings", err)
		return
	}
	echo := commentEchoFrom(ctx)
	if echo != nil && echo.EntityID != id {
		echo = nil
	}
	q := r.URL.Query()
	page := 1
	if n, err := strconv.Atoi(q.Get("cpage")); err == nil && n > 1 {
		page = min(n, render.MaxQueryPage)
	}
	if page >= 2 {
		v.Page.NoIndex = true // the canonical URL stays the first page
	}
	tp, err := c.reader.Thread(ctx, id, page)
	if err != nil {
		fail("comments: thread", err)
		return
	}
	counts, err := c.reader.Counts(ctx, []uuid.UUID{id})
	if err != nil {
		fail("comments: counts", err)
		return
	}

	// The viewer: a device cookie the service accepts. It decides own items and,
	// for dead comments, the owner plugin's show-dead reveal.
	var cid uuid.UUID
	var known bool
	if cookie, err := r.Cookie(identity.CookieName); err == nil && c.device != nil {
		cid, known = c.device(ctx, cookie.Value)
	}
	reveal := known && cfg.OwnerPlugin != "" && c.showDead != nil && c.showDead(ctx, cfg.OwnerPlugin, cid)
	if reveal && v.Uncacheable != nil {
		*v.Uncacheable = true // a dead body must never enter the shared cache
	}

	maxDepth := cfg.Policy.Depth(set.MaxDepth)
	window := cfg.Policy.EditWindow()
	newest := cfg.Order.Sort(set.Sort) == comment.SortNewest
	nodes := newCommentTree()
	replyable := map[string]string{} // id -> author of the items a reply may target
	for _, tc := range tp.Comments {
		it := commentItemAs(tc, reveal)
		canReply := tc.Depth < maxDepth && !it.Dead
		if canReply {
			it.ReplyURL = v.Path + "?creply=" + tc.ID.String() + "#hm-comment-form"
		}
		nodes.add(tc.ParentID, it, false)
		if canReply {
			replyable[it.ID] = it.Author
		}
	}

	// Own non-public items, only for a cookie the service accepts. The render
	// is then private: it must not enter the shared cache.
	if known {
		own, err := c.reader.ViewerComments(ctx, id, cid)
		if err != nil {
			fail("comments: own items", err)
			return
		}
		now := c.now()
		nodes.rootsFirst = newest
		for _, tc := range own {
			if n := nodes.byID[tc.ID.String()]; n != nil && tc.Status == comment.StatusDead {
				// Already in the thread as a placeholder: the author sees their own body, once.
				n.item = commentItemAs(tc, true)
				n.item.Own = true
				continue
			}
			it := commentItemAs(tc, true)
			it.Own = true
			if !it.Dead && now.Sub(tc.CreatedAt) < window {
				setEditable(&it, tc.ID)
			}
			// A reply whose parent is off this page shows on the first page only.
			nodes.add(tc.ParentID, it, page == 1)
		}
		// Their approved comments are already in the thread; inside the
		// edit window they get the same actions.
		editable, err := c.reader.EditableComments(ctx, id, cid)
		if err != nil {
			fail("comments: editable items", err)
			return
		}
		marked := 0
		for _, tc := range editable {
			if n := nodes.byID[tc.ID.String()]; n != nil && now.Sub(tc.CreatedAt) < window {
				setEditable(&n.item, tc.ID)
				marked++
			}
		}
		if (len(own) > 0 || marked > 0) && v.Uncacheable != nil {
			*v.Uncacheable = true
		}
	}

	thread := &render.CommentThread{
		Items: nodes.items(), Total: counts[id].Approved, Page: page, Pages: max(tp.Pages, page), Newest: newest,
	}
	if tp.HasMore {
		thread.NextURL = v.Path + "?cpage=" + strconv.Itoa(page+1) + "#hm-comments"
	}
	if page > 1 {
		thread.PrevURL = v.Path
		if page > 2 {
			thread.PrevURL += "?cpage=" + strconv.Itoa(page-1)
		}
		thread.PrevURL += "#hm-comments"
	}
	thread.PageLinks = commentPageLinks(v.Path, page, thread.Pages)
	if n := q.Get("cnotice"); commentNotices[n] {
		thread.Notice = n
	}
	if e := q.Get("cerror"); commentErrors[e] {
		thread.Error = e
	}
	if echo != nil && commentErrors[echo.Code] {
		thread.Error = echo.Code
		if echo.Code == commentsubmit.CodeDraft {
			thread.ErrorText = echo.Message // only the POST response carries it, never a URL
		}
	}
	v.Comments = thread
	v.CommentCount = counts[id].Approved
	v.CommentsOpen = mode == comment.ModeOpen
	if !v.CommentsOpen {
		return
	}
	p := render.CommentFormPlaceholders
	p.Action = "/-/comments/" + id.String()
	p.MaxBody = commentsubmit.MaxBodyRunes
	if author, ok := replyable[q.Get("creply")]; ok {
		p.ParentID, p.ReplyTo = q.Get("creply"), author
	}
	if echo != nil {
		if echo.ParentID != "" {
			p.ParentID, p.ReplyTo = echo.ParentID, replyable[echo.ParentID]
		}
		p.Focus = commentFocus(echo.Code)
		if t := echo.Typed; t != nil {
			vp := render.CommentValuePlaceholders
			if t.Body != "" {
				p.BodyValue = vp.BodyValue
			}
			if t.Name != "" {
				p.NameValue = vp.NameValue
			}
			if t.Email != "" {
				p.EmailValue = vp.EmailValue
			}
			p.Notify = t.Notify
		}
		if v.Uncacheable != nil {
			*v.Uncacheable = true
		}
	}
	if set.Captcha && c.captcha != nil {
		if w := c.captcha(ctx); w != nil && w.provider != "" {
			p.Captcha = &render.CommentCaptcha{Provider: w.provider, SiteKey: w.siteKey}
		}
	}
	// A failed policy read renders the locked form for this visitor only: a
	// guessed state must not enter the shared page cache.
	lockCommentForm(ctx, c.policy, id, &p, func(ctx context.Context, msg string, err error) {
		c.logf(ctx, msg, err)
		if v.Uncacheable != nil {
			*v.Uncacheable = true
		}
	})
	v.CommentForm = &p
}

// setEditable gives an own item its edit and delete actions.
func setEditable(it *render.CommentItem, id uuid.UUID) {
	it.Editable = true
	it.EditAction = "/-/comments/edit/" + id.String()
	it.DeleteAction = "/-/comments/delete/" + id.String()
}

// commentItem maps a stored comment for a viewer with no dead reveal.
func commentItem(tc comment.ThreadComment) render.CommentItem { return commentItemAs(tc, false) }

// commentItemAs maps a stored comment. A dead one keeps its place in the tree
// but shows no author and no body unless reveal: the author, or a viewer the
// owner plugin flagged.
func commentItemAs(tc comment.ThreadComment, reveal bool) render.CommentItem {
	it := render.CommentItem{
		ID: tc.ID.String(), Author: tc.AuthorName, IsSiteAuthor: tc.IsSiteAuthor,
		Body:      template.HTML(tc.BodyHTML), // #nosec G203 -- stored commentfmt output
		CreatedAt: tc.CreatedAt, Depth: tc.Depth,
	}
	if tc.Status == comment.StatusDead {
		it.Dead = true
		if !reveal {
			it.Author, it.Body, it.IsSiteAuthor = "", "", false
		}
	}
	return it
}

// commentTree nests a flat, render-ordered list by parent.
type commentTree struct {
	roots      []*commentNode
	byID       map[string]*commentNode
	rootsFirst bool // new roots go before the existing ones (own items, newest sort)
}

type commentNode struct {
	item render.CommentItem
	kids []*commentNode
}

func newCommentTree() *commentTree { return &commentTree{byID: map[string]*commentNode{}} }

// add attaches it under its parent when the parent is in the tree. A reply
// whose parent is missing becomes a root when orphanRoot is set and is dropped
// otherwise.
func (t *commentTree) add(parent *uuid.UUID, it render.CommentItem, orphanRoot bool) {
	n := &commentNode{item: it}
	if parent != nil {
		if p, ok := t.byID[parent.String()]; ok {
			p.kids = append(p.kids, n)
			t.byID[it.ID] = n
			return
		}
		if !orphanRoot {
			return
		}
	}
	t.byID[it.ID] = n
	if t.rootsFirst {
		t.roots = append([]*commentNode{n}, t.roots...)
	} else {
		t.roots = append(t.roots, n)
	}
}

func (t *commentTree) items() []render.CommentItem { return convertNodes(t.roots) }

func convertNodes(ns []*commentNode) []render.CommentItem {
	if len(ns) == 0 {
		return nil
	}
	out := make([]render.CommentItem, len(ns))
	for i, n := range ns {
		out[i] = n.item
		out[i].Replies = convertNodes(n.kids)
	}
	return out
}

// commentPageURL is the link to one page of the thread; page 1 is the entity URL.
func commentPageURL(path string, page int) string {
	if page <= 1 {
		return path + "#hm-comments"
	}
	return path + "?cpage=" + strconv.Itoa(page) + "#hm-comments"
}

// commentPageLinks lists the pager entries: the first page, the last page and
// the pages next to the current one, with a gap entry (Page 0) between runs.
func commentPageLinks(path string, page, pages int) []render.CommentPageLink {
	if pages < 2 {
		return nil
	}
	var out []render.CommentPageLink
	last := 0
	for p := 1; p <= pages; p++ {
		if p != 1 && p != pages && (p < page-2 || p > page+2) {
			continue
		}
		if last != 0 && p > last+1 {
			out = append(out, render.CommentPageLink{})
		}
		out = append(out, render.CommentPageLink{Page: p, URL: commentPageURL(path, p), Current: p == page})
		last = p
	}
	return out
}
