package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/render"
)

type fakeCommentReader struct {
	set       comment.Settings
	mode      comment.Mode
	modeErr   error
	policy    comment.Policy
	order     comment.Order
	thread    comment.ThreadPage
	threadErr error
	approved  int
	own       []comment.ThreadComment
	editable  []comment.ThreadComment
	owner     string // EntityConfig.OwnerPlugin

	threadCalls, countCalls, ownCalls int
	gotPage                           int
}

func (f *fakeCommentReader) Settings(context.Context) (comment.Settings, error) { return f.set, nil }
func (f *fakeCommentReader) EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error) {
	return comment.EntityConfig{Mode: f.mode, Order: f.order, Policy: f.policy, OwnerPlugin: f.owner}, f.modeErr
}
func (f *fakeCommentReader) Thread(_ context.Context, _ uuid.UUID, page int) (comment.ThreadPage, error) {
	f.threadCalls++
	f.gotPage = page
	return f.thread, f.threadErr
}
func (f *fakeCommentReader) Counts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]comment.EntityCounts, error) {
	f.countCalls++
	return map[uuid.UUID]comment.EntityCounts{ids[0]: {Approved: f.approved}}, nil
}
func (f *fakeCommentReader) ViewerComments(context.Context, uuid.UUID, uuid.UUID) ([]comment.ThreadComment, error) {
	f.ownCalls++
	return f.own, nil
}

func (f *fakeCommentReader) EditableComments(context.Context, uuid.UUID, uuid.UUID) ([]comment.ThreadComment, error) {
	return f.editable, nil
}

type viewFixture struct {
	fake              *fakeCommentReader
	cv                *commentView
	id                uuid.UUID
	root, reply, deep comment.ThreadComment
}

func newViewFixture() *viewFixture {
	id := uuid.New()
	now := time.Now()
	mk := func(parent *uuid.UUID, depth int, name string) comment.ThreadComment {
		return comment.ThreadComment{ID: uuid.New(), ParentID: parent, Depth: depth, AuthorName: name, BodyHTML: "<p>" + name + "</p>", CreatedAt: now}
	}
	f := &viewFixture{id: id}
	f.root = mk(nil, 1, "root")
	f.reply = mk(&f.root.ID, 2, "reply")
	f.deep = mk(nil, 1, "second")
	f.fake = &fakeCommentReader{
		set:      comment.Settings{MaxDepth: 2, PageSize: 50, Sort: comment.SortNewest},
		mode:     comment.ModeOpen,
		thread:   comment.ThreadPage{Page: 1, Comments: []comment.ThreadComment{f.root, f.reply, f.deep}},
		approved: 3,
	}
	f.cv = &commentView{
		reader: f.fake, now: time.Now,
		logf: func(context.Context, string, error) {},
	}
	return f
}

func (f *viewFixture) run(url string, mut ...func(*http.Request)) *render.PageView {
	r := httptest.NewRequest(http.MethodGet, url, nil)
	for _, m := range mut {
		m(r)
	}
	v := &render.PageView{EntityID: f.id.String(), Path: "/post", Uncacheable: new(bool)}
	f.cv.fill(context.Background(), r, v)
	return v
}

func TestCommentViewOffAndHidden(t *testing.T) {
	f := newViewFixture()
	f.fake.mode = comment.ModeOff
	if v := f.run("/post"); v.Comments != nil || v.CommentForm != nil {
		t.Fatal("off: want no comments")
	}
	f.fake.mode = comment.ModeHidden
	if v := f.run("/post"); v.Comments != nil {
		t.Fatal("hidden: want no comments")
	}
	f.fake.mode, f.fake.modeErr = comment.ModeOpen, comment.ErrNotFound
	if v := f.run("/post"); v.Comments != nil {
		t.Fatal("unknown entity: want no comments")
	}
	// no entity, preview
	f.fake.modeErr = nil
	v := &render.PageView{Path: "/"}
	f.cv.fill(context.Background(), httptest.NewRequest("GET", "/", nil), v)
	if v.Comments != nil {
		t.Fatal("no entity: want no comments")
	}
	v = &render.PageView{EntityID: f.id.String(), Preview: true}
	f.cv.fill(context.Background(), httptest.NewRequest("GET", "/", nil), v)
	if v.Comments != nil {
		t.Fatal("preview: want no comments")
	}
}

func TestCommentViewOpenTree(t *testing.T) {
	f := newViewFixture()
	v := f.run("/post")
	if v.Comments == nil || !v.CommentsOpen || v.CommentForm == nil {
		t.Fatal("open: want thread, open flag and form")
	}
	if v.CommentCount != 3 || v.Comments.Total != 3 {
		t.Fatalf("count = %d/%d", v.CommentCount, v.Comments.Total)
	}
	items := v.Comments.Items
	if len(items) != 2 || len(items[0].Replies) != 1 || items[0].Replies[0].Author != "reply" {
		t.Fatalf("tree = %+v", items)
	}
	if items[0].ReplyURL != "/post?creply="+f.root.ID.String()+"#hm-comment-form" {
		t.Fatalf("ReplyURL = %q", items[0].ReplyURL)
	}
	if items[0].Replies[0].ReplyURL != "" {
		t.Fatal("reply at the max depth must have no reply link")
	}
	if string(items[0].Body) != "<p>root</p>" {
		t.Fatalf("body = %q", items[0].Body)
	}
	form := v.CommentForm
	if form.Action != "/-/comments/"+f.id.String() || form.Token != render.CommentFormPlaceholders.Token ||
		form.Body != render.CommentFormPlaceholders.Body || form.MaxBody != 5000 {
		t.Fatalf("form = %+v", form)
	}
	if f.fake.threadCalls != 1 || f.fake.countCalls != 1 {
		t.Fatalf("queries: thread %d counts %d, want 1 each", f.fake.threadCalls, f.fake.countCalls)
	}
	if *v.Uncacheable {
		t.Fatal("a plain view must stay cacheable")
	}
	if v.Page.NoIndex || v.Comments.PrevURL != "" || v.Comments.NextURL != "" {
		t.Fatal("first page: no noindex, no pager")
	}
}

func TestCommentViewClosedShowsNoForm(t *testing.T) {
	f := newViewFixture()
	f.fake.mode = comment.ModeClosed
	v := f.run("/post")
	if v.Comments == nil || v.CommentForm != nil || v.CommentsOpen {
		t.Fatalf("closed: thread %v form %v open %v", v.Comments != nil, v.CommentForm != nil, v.CommentsOpen)
	}
}

func TestCommentViewPaging(t *testing.T) {
	f := newViewFixture()
	f.fake.thread.HasMore = true
	f.fake.thread.Pages = 3
	v := f.run("/post?cpage=2")
	if f.fake.gotPage != 2 || !v.Page.NoIndex {
		t.Fatalf("page %d noindex %v", f.fake.gotPage, v.Page.NoIndex)
	}
	if v.Comments.PrevURL != "/post#hm-comments" || v.Comments.NextURL != "/post?cpage=3#hm-comments" {
		t.Fatalf("pager = %q %q", v.Comments.PrevURL, v.Comments.NextURL)
	}
	var got []string
	for _, l := range v.Comments.PageLinks {
		got = append(got, fmt.Sprintf("%d %s %v", l.Page, l.URL, l.Current))
	}
	want := []string{"1 /post#hm-comments false", "2 /post?cpage=2#hm-comments true", "3 /post?cpage=3#hm-comments false"}
	if !slices.Equal(got, want) {
		t.Fatalf("page links = %q", got)
	}
	v = f.run("/post?cpage=3")
	if v.Comments.PrevURL != "/post?cpage=2#hm-comments" {
		t.Fatalf("PrevURL = %q", v.Comments.PrevURL)
	}
	for _, bad := range []string{"abc", "-4", "0", ""} {
		f.run("/post?cpage=" + bad)
		if f.fake.gotPage != 1 {
			t.Fatalf("cpage=%q -> page %d", bad, f.fake.gotPage)
		}
	}
}

func TestCommentViewReplyTarget(t *testing.T) {
	f := newViewFixture()
	v := f.run("/post?creply=" + f.root.ID.String())
	if v.CommentForm.ParentID != f.root.ID.String() || v.CommentForm.ReplyTo != "root" {
		t.Fatalf("form = %+v", v.CommentForm)
	}
	for name, target := range map[string]string{
		"max depth": f.reply.ID.String(), "unknown": uuid.NewString(), "junk": "x\"><script>",
	} {
		if v := f.run("/post?creply=" + target); v.CommentForm.ParentID != "" || v.CommentForm.ReplyTo != "" {
			t.Fatalf("%s: reply target accepted: %+v", name, v.CommentForm)
		}
	}
}

func TestCommentViewNoticeAndErrorWhitelist(t *testing.T) {
	f := newViewFixture()
	v := f.run("/post?cnotice=check_email&cerror=body_long")
	if v.Comments.Notice != "check_email" || v.Comments.Error != "body_long" {
		t.Fatalf("codes = %q %q", v.Comments.Notice, v.Comments.Error)
	}
	v = f.run("/post?cnotice=%3Cb%3E&cerror=whatever")
	if v.Comments.Notice != "" || v.Comments.Error != "" {
		t.Fatalf("unknown codes passed: %q %q", v.Comments.Notice, v.Comments.Error)
	}
}

func TestCommentViewOwnItemsNeedAVerifiedCookie(t *testing.T) {
	f := newViewFixture()
	f.fake.own = []comment.ThreadComment{{
		ID: uuid.New(), ParentID: &f.root.ID, Depth: 2, AuthorName: "mine", BodyHTML: "<p>mine</p>", CreatedAt: time.Now(),
	}}
	cookie := func(val string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: val}) }
	}
	f.cv.device = func(_ context.Context, val string) (uuid.UUID, bool) { return uuid.New(), val == "good" }

	for name, mut := range map[string][]func(*http.Request){"no cookie": nil, "rejected cookie": {cookie("forged")}} {
		v := f.run("/post", mut...)
		if f.fake.ownCalls != 0 || len(v.Comments.Items[0].Replies) != 1 || *v.Uncacheable {
			t.Fatalf("%s: own items read or shown", name)
		}
	}
	v := f.run("/post", cookie("good"))
	if f.fake.ownCalls != 1 {
		t.Fatalf("ViewerComments calls = %d", f.fake.ownCalls)
	}
	replies := v.Comments.Items[0].Replies
	if len(replies) != 2 || replies[1].Author != "mine" || !replies[1].Own || !replies[1].Editable {
		t.Fatalf("replies = %+v", replies)
	}
	if replies[1].EditAction != "/-/comments/edit/"+replies[1].ID || replies[1].DeleteAction != "/-/comments/delete/"+replies[1].ID {
		t.Fatalf("actions = %q %q", replies[1].EditAction, replies[1].DeleteAction)
	}
	if !*v.Uncacheable {
		t.Fatal("a view with own items must not be cached")
	}
	// Past the edit window the item stays but is no longer editable.
	f.fake.own[0].CreatedAt = time.Now().Add(-time.Hour)
	if r := f.run("/post", cookie("good")).Comments.Items[0].Replies[1]; r.Editable || r.EditAction != "" {
		t.Fatal("item past the edit window is editable")
	}
	// An own reply to a comment on another page shows on page 1 only.
	f.fake.own[0].ParentID = &[]uuid.UUID{uuid.New()}[0]
	if n := len(f.run("/post", cookie("good")).Comments.Items); n != 3 {
		t.Fatalf("page 1 roots = %d, want the orphan own reply as a root", n)
	}
	if n := len(f.run("/post?cpage=2", cookie("good")).Comments.Items); n != 2 {
		t.Fatalf("page 2 roots = %d, want 2", n)
	}
}

func TestCommentViewErrorKeepsPageOutOfCache(t *testing.T) {
	f := newViewFixture()
	f.fake.threadErr = errors.New("db down")
	v := f.run("/post")
	if v.Comments != nil || v.CommentForm != nil || !*v.Uncacheable {
		t.Fatalf("error: comments %v uncacheable %v", v.Comments != nil, *v.Uncacheable)
	}
}

func TestCommentViewCaptcha(t *testing.T) {
	f := newViewFixture()
	f.cv.captcha = func(context.Context) *captchaWidget { return &captchaWidget{provider: "turnstile", siteKey: "k"} }
	if v := f.run("/post"); v.CommentForm.Captcha != nil {
		t.Fatal("captcha shown while the setting is off")
	}
	f.fake.set.Captcha = true
	v := f.run("/post")
	if v.CommentForm.Captcha == nil || v.CommentForm.Captcha.Provider != "turnstile" || v.CommentForm.Captcha.SiteKey != "k" {
		t.Fatalf("captcha = %+v", v.CommentForm.Captcha)
	}
	f.cv.captcha = func(context.Context) *captchaWidget { return &captchaWidget{} }
	if v := f.run("/post"); v.CommentForm.Captcha != nil {
		t.Fatal("captcha shown without a usable provider")
	}
}

func TestCommentViewOwnApprovedGetsActionsInsideTheWindow(t *testing.T) {
	f := newViewFixture()
	f.cv.device = func(context.Context, string) (uuid.UUID, bool) { return uuid.New(), true }
	cookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"}) }
	roots := len(f.run("/post").Comments.Items)
	mine := f.root
	mine.CreatedAt = time.Now().Add(-time.Minute)
	f.fake.editable = []comment.ThreadComment{mine}

	v := f.run("/post", cookie)
	root := v.Comments.Items[0]
	if !root.Editable || root.EditAction != "/-/comments/edit/"+root.ID || root.DeleteAction != "/-/comments/delete/"+root.ID {
		t.Fatalf("own approved item = %+v", root)
	}
	if !*v.Uncacheable {
		t.Fatal("a view with an editable own item must not be cached")
	}
	if len(v.Comments.Items) != roots {
		t.Fatalf("approved item duplicated: %d roots, want %d", len(v.Comments.Items), roots)
	}

	// Window over, or somebody else's item: nothing is offered.
	f.fake.editable[0].CreatedAt = time.Now().Add(-time.Hour)
	if r := f.run("/post", cookie).Comments.Items[0]; r.Editable {
		t.Fatal("editable after the window")
	}
	f.fake.editable = []comment.ThreadComment{{ID: uuid.New(), CreatedAt: time.Now()}}
	if v := f.run("/post", cookie); v.Comments.Items[0].Editable || *v.Uncacheable {
		t.Fatal("an item that is not on the page was marked")
	}
}

func TestCommentPageLinksGaps(t *testing.T) {
	var got []string
	for _, l := range commentPageLinks("/p", 20, 40) {
		got = append(got, strconv.Itoa(l.Page))
	}
	if want := "1 0 18 19 20 21 22 0 40"; strings.Join(got, " ") != want {
		t.Fatalf("links = %q, want %q", got, want)
	}
	if commentPageLinks("/p", 1, 1) != nil {
		t.Fatal("one page: no pager")
	}
}

func TestCommentViewEcho(t *testing.T) {
	f := newViewFixture()
	echo := commentEcho{EntityID: f.id, Code: "body_long", ParentID: f.root.ID.String(),
		Typed: &commentsubmit.Typed{Body: "x", Name: "Ana", Notify: true}}
	run := func(e commentEcho) *render.PageView {
		r := httptest.NewRequest(http.MethodGet, "/post", nil)
		v := &render.PageView{EntityID: f.id.String(), Path: "/post", Uncacheable: new(bool)}
		f.cv.fill(withCommentEcho(context.Background(), e), r, v)
		return v
	}
	v := run(echo)
	vp := render.CommentValuePlaceholders
	form := v.CommentForm
	if v.Comments.Error != "body_long" || form.ParentID != f.root.ID.String() || form.ReplyTo != "root" {
		t.Fatalf("error/parent: %q %q %q", v.Comments.Error, form.ParentID, form.ReplyTo)
	}
	if form.BodyValue != vp.BodyValue || form.NameValue != vp.NameValue || form.EmailValue != "" || !form.Notify {
		t.Fatalf("values: %+v", form)
	}
	if !*v.Uncacheable {
		t.Fatal("echo render must be uncacheable")
	}

	echo.EntityID = uuid.New()
	v = run(echo)
	if v.Comments.Error != "" || v.CommentForm.ParentID != "" || v.CommentForm.BodyValue != "" || v.CommentForm.Notify || *v.Uncacheable {
		t.Fatalf("echo for another entity applied: %+v", v.CommentForm)
	}
}

func TestCommentViewFollowsTypePolicy(t *testing.T) {
	f := newViewFixture() // site: max depth 2, newest first
	f.fake.policy = comment.Policy{Enabled: true, MaxDepth: 3, EditWindowMinutes: 120}
	v := f.run("/post")
	if v.Comments.Items[0].Replies[0].ReplyURL == "" {
		t.Fatal("a reply at depth 2 must be replyable when the type allows depth 3")
	}
	if !v.Comments.Newest {
		t.Fatal("no type order: the site sort applies")
	}

	f.fake.order = comment.OrderOldest
	if f.run("/post").Comments.Newest {
		t.Fatal("type order oldest must override the site sort")
	}
	f.fake.order = comment.OrderRank
	if !f.run("/post").Comments.Newest {
		t.Fatal("type order rank falls back to the site sort")
	}

	f.cv.device = func(context.Context, string) (uuid.UUID, bool) { return uuid.New(), true }
	cookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"}) }
	mine := f.root
	mine.CreatedAt = time.Now().Add(-time.Hour)
	f.fake.editable = []comment.ThreadComment{mine}
	if r := f.run("/post", cookie).Comments.Items[0]; !r.Editable {
		t.Fatal("an hour-old comment is editable inside the type's 120 minute window")
	}
	f.fake.policy.EditWindowMinutes = 30
	if r := f.run("/post", cookie).Comments.Items[0]; r.Editable {
		t.Fatal("editable after the type's 30 minute window")
	}
}

func deadFixture() (*viewFixture, comment.ThreadComment) {
	f := newViewFixture()
	f.root.Status = comment.StatusDead
	f.fake.owner = "example"
	f.fake.thread.Comments = []comment.ThreadComment{f.root, f.reply, f.deep}
	return f, f.root
}

func withDevice(f *viewFixture, cid uuid.UUID) func(*http.Request) {
	f.cv.device = func(context.Context, string) (uuid.UUID, bool) { return cid, true }
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"}) }
}

func TestCommentViewDeadPlaceholderForAnonymous(t *testing.T) {
	f, dead := deadFixture()
	f.cv.showDead = func(context.Context, string, uuid.UUID) bool { return true } // never asked without a viewer
	v := f.run("/post")
	root := v.Comments.Items[0]
	if !root.Dead || root.Author != "" || root.Body != "" || root.IsSiteAuthor || root.ReplyURL != "" {
		t.Fatalf("dead item leaks or is repliable: %+v", root)
	}
	if root.ID != dead.ID.String() || len(root.Replies) != 1 || root.Replies[0].Author != "reply" {
		t.Fatalf("the reply of a dead comment must stay nested: %+v", root)
	}
	if *v.Uncacheable {
		t.Fatal("the anonymous placeholder is the shared, cacheable page")
	}
	if strings.Contains(string(v.Comments.Items[0].Body), dead.BodyHTML) {
		t.Fatal("dead body in the anonymous render")
	}
}

func TestCommentViewDeadRevealIsUncacheable(t *testing.T) {
	f, _ := deadFixture()
	cid := uuid.New()
	var asked string
	f.cv.showDead = func(_ context.Context, plugin string, got uuid.UUID) bool {
		asked = plugin
		return got == cid
	}
	v := f.run("/post", withDevice(f, cid))
	root := v.Comments.Items[0]
	if !root.Dead || root.Author != "root" || root.Body != "<p>root</p>" || asked != "example" {
		t.Fatalf("reveal: %+v asked %q", root, asked)
	}
	if !*v.Uncacheable {
		t.Fatal("a reveal render must not enter the shared cache")
	}
	f.fake.owner = "" // a content type nobody owns has no reveal
	if v = f.run("/post", withDevice(f, cid)); v.Comments.Items[0].Body != "" {
		t.Fatal("reveal without an owner plugin")
	}
}

func TestCommentViewDeadOwnBodyShownOnce(t *testing.T) {
	f, dead := deadFixture()
	cid := uuid.New()
	f.fake.own = []comment.ThreadComment{dead}
	v := f.run("/post", withDevice(f, cid))
	if len(v.Comments.Items) != 2 {
		t.Fatalf("own dead comment duplicated: %d roots", len(v.Comments.Items))
	}
	var mine *render.CommentItem
	for i := range v.Comments.Items {
		if v.Comments.Items[i].ID == dead.ID.String() {
			mine = &v.Comments.Items[i]
		}
	}
	if mine == nil || !mine.Own || !mine.Dead || mine.Body != "<p>root</p>" || mine.Editable || len(mine.Replies) != 1 {
		t.Fatalf("own dead item = %+v", mine)
	}
	if !*v.Uncacheable {
		t.Fatal("own dead view must be private")
	}
}

func TestCommentViewLocksFormByTypePolicy(t *testing.T) {
	f := newViewFixture()
	if v := f.run("/post"); v.CommentForm == nil || v.CommentForm.Locked {
		t.Fatalf("policy off: %+v", v.CommentForm)
	}
	f.fake.policy = comment.Policy{Enabled: true}
	v := f.run("/post")
	if v.CommentForm == nil || !v.CommentForm.Locked || v.CommentForm.SignInURL != commentSignInPath {
		t.Fatalf("policy on: %+v", v.CommentForm)
	}
}
