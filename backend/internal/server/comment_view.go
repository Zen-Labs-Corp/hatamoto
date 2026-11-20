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
	EntityMode(context.Context, uuid.UUID) (comment.Mode, error)
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
	now     func() time.Time
	logf    func(ctx context.Context, msg string, err error)
}

// Codes the theme turns into text; anything else in the query is ignored.
var (
	commentNotices = map[string]bool{"check_email": true, "posted": true}
	commentErrors  = map[string]bool{
		commentsubmit.CodeBodyEmpty: true, commentsubmit.CodeBodyLong: true, commentsubmit.CodeName: true,
		commentsubmit.CodeEmail: true, commentsubmit.CodeEmailDomain: true, "closed": true,
	}
)

// fillComments sets the comment parts of v for a real entity view; see commentView.fill.
func (s *Server) fillComments(ctx context.Context, r *http.Request, rr *routeResult, v *render.PageView) {
	if s.comments == nil {
		return
	}
	cv := commentView{reader: s.comments, now: time.Now, captcha: s.captchaWidgetFor}
	if s.commentIdentity != nil {
		cv.device = s.commentIdentity.Device
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
	mode, err := c.reader.EntityMode(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		return
	}
	if err != nil {
		fail("comments: entity mode", err)
		return
	}
	if mode != comment.ModeOpen && mode != comment.ModeClosed {
		return
	}
	set, err := c.reader.Settings(ctx)
	if err != nil {
		fail("comments: settings", err)
		return
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

	nodes := newCommentTree()
	replyable := map[string]string{} // id -> author of the items a reply may target
	for _, tc := range tp.Comments {
		it := commentItem(tc)
		if tc.Depth < set.MaxDepth {
			it.ReplyURL = v.Path + "?creply=" + tc.ID.String() + "#comment-form"
		}
		nodes.add(tc.ParentID, it, false)
		if tc.Depth < set.MaxDepth {
			replyable[it.ID] = it.Author
		}
	}

	// Own non-public items, only for a cookie the service accepts. The render
	// is then private: it must not enter the shared cache.
	if cookie, err := r.Cookie(identity.CookieName); err == nil && c.device != nil {
		if cid, ok := c.device(ctx, cookie.Value); ok {
			own, err := c.reader.ViewerComments(ctx, id, cid)
			if err != nil {
				fail("comments: own items", err)
				return
			}
			now := c.now()
			nodes.rootsFirst = set.Sort == comment.SortNewest
			for _, tc := range own {
				it := commentItem(tc)
				it.Own = true
				if now.Sub(tc.CreatedAt) < comment.EditWindow {
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
				if n := nodes.byID[tc.ID.String()]; n != nil && now.Sub(tc.CreatedAt) < comment.EditWindow {
					setEditable(&n.item, tc.ID)
					marked++
				}
			}
			if (len(own) > 0 || marked > 0) && v.Uncacheable != nil {
				*v.Uncacheable = true
			}
		}
	}

	thread := &render.CommentThread{
		Items: nodes.items(), Total: counts[id].Approved, Page: page, Newest: set.Sort == comment.SortNewest,
	}
	if tp.HasMore {
		thread.NextURL = v.Path + "?cpage=" + strconv.Itoa(page+1) + "#comments"
	}
	if page > 1 {
		thread.PrevURL = v.Path
		if page > 2 {
			thread.PrevURL += "?cpage=" + strconv.Itoa(page-1)
		}
		thread.PrevURL += "#comments"
	}
	if n := q.Get("cnotice"); commentNotices[n] {
		thread.Notice = n
	}
	if e := q.Get("cerror"); commentErrors[e] {
		thread.Error = e
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
	if set.Captcha && c.captcha != nil {
		if w := c.captcha(ctx); w != nil && w.provider != "" {
			p.Captcha = &render.CommentCaptcha{Provider: w.provider, SiteKey: w.siteKey}
		}
	}
	v.CommentForm = &p
}

// setEditable gives an own item its edit and delete actions.
func setEditable(it *render.CommentItem, id uuid.UUID) {
	it.Editable = true
	it.EditAction = "/-/comments/edit/" + id.String()
	it.DeleteAction = "/-/comments/delete/" + id.String()
}

func commentItem(tc comment.ThreadComment) render.CommentItem {
	return render.CommentItem{
		ID: tc.ID.String(), Author: tc.AuthorName, IsSiteAuthor: tc.IsSiteAuthor,
		Body:      template.HTML(tc.BodyHTML), // #nosec G203 -- stored commentfmt output
		CreatedAt: tc.CreatedAt, Depth: tc.Depth,
	}
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
