package admin

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

const modEntityID = "5c1f0f0e-1b7a-4d86-9d5e-2a1f7c0b3e11"

func modRows() []CommentRow {
	return []CommentRow{{
		ID: commentsTestID, EntityID: modEntityID, EntityTitle: "Hello world", Status: "pending",
		BodyRaw: "Nice post", AuthorName: "Ana", CommenterID: "c1", Trust: "new",
		Reasons: []string{"contains a link", "new commenter"}, CreatedAt: time.Now().Add(-2 * time.Hour),
	}}
}

func modGet(t *testing.T, p *perfRig, token, target string) string {
	t.Helper()
	rec := p.do("GET", target, token, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d", target, rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestCommentsListOwnerAndEditor(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows(), counts: CommentCounts{Pending: 1}})
	for _, tok := range []string{"owner-token", "editor-token"} {
		body := modGet(t, p, tok, "/admin/comments")
		if !strings.Contains(body, "Ana") || !strings.Contains(body, "1 waiting for review") {
			t.Errorf("%s: list lacks the row or the count", tok)
		}
	}
}

func TestCommentsListHeldWhy(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows(), counts: CommentCounts{Pending: 1}})
	body := modGet(t, p, "owner-token", "/admin/comments")
	for _, want := range []string{"Held: contains a link, first comment", ">new<", "0 approved"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestCommentsListCountsOnlyUnfiltered(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows(), counts: CommentCounts{Pending: 7, Approved: 4242, Spam: 4, Trash: 5}})
	if body := modGet(t, p, "owner-token", "/admin/comments"); !strings.Contains(body, "4242") {
		t.Error("unfiltered tabs lack their counts")
	}
	if body := modGet(t, p, "owner-token", "/admin/comments?entity="+modEntityID); strings.Contains(body, "4242") {
		t.Error("a filtered view shows tab counts")
	}
}

func TestCommentsListHasMore(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows(), hasMore: true})
	body := modGet(t, p, "owner-token", "/admin/comments?q=nice")
	if !strings.Contains(body, "page=2") {
		t.Error("a page with more rows has no next-page link")
	}
}

func TestCommentsListStatusWhitelist(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	body := modGet(t, p, "owner-token", "/admin/comments?status=bogus")
	if svc.lastFilter.Status != "pending" {
		t.Errorf("queue status = %q, want pending", svc.lastFilter.Status)
	}
	if !strings.Contains(body, `value="pending" checked`) && !strings.Contains(body, `checked value="pending"`) && !strings.Contains(body, `aria-pressed="true"`) {
		t.Error("the Pending tab is not pressed")
	}
	modGet(t, p, "owner-token", "/admin/comments?status=draft")
	if svc.lastFilter.Status != "pending" {
		t.Errorf("draft status reached the queue: %q", svc.lastFilter.Status)
	}
}

func TestCommentsListNoURL(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows()})
	body := modGet(t, p, "owner-token", "/admin/comments")
	for _, bad := range []string{"author_url", "website"} {
		if strings.Contains(body, bad) {
			t.Errorf("page has %q", bad)
		}
	}
}

func TestCommentsListQueueError(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{err: ErrCommentsUnsupported})
	if body := modGet(t, p, "owner-token", "/admin/comments"); !strings.Contains(body, "could not be loaded") {
		t.Error("a queue error shows no error state")
	}
}

func TestHeldWhyUnknownCode(t *testing.T) {
	t.Parallel()

	got := heldWhy(CommentRow{Status: "pending", Reasons: []string{"plugin said no", "spam score"}, Score: 4})
	if want := "Held: plugin said no, high spam score · score 4"; got != want {
		t.Errorf("heldWhy = %q, want %q", got, want)
	}
	if got := heldWhy(CommentRow{Status: "approved", Reasons: []string{"new commenter"}}); got != "" {
		t.Errorf("approved heldWhy = %q, want empty", got)
	}
}

func modPost(t *testing.T, p *perfRig, token, path string, form url.Values) string {
	t.Helper()
	rec := p.do("POST", "/admin/comments/"+commentsTestID+path, token, form, nil)
	if rec.Code != 303 {
		t.Fatalf("POST %s = %d, want 303", path, rec.Code)
	}
	return rec.Header().Get("Location")
}

func TestCommentApprove(t *testing.T) {
	t.Parallel()

	for _, tok := range []string{"owner-token", "editor-token"} {
		svc := &fakeComments{rows: modRows()}
		p := newCommentsRig(t, svc)
		modPost(t, p, tok, "/approve", url.Values{})
		if !slices.Equal(svc.calls, []string{"approve"}) || !slices.Equal(svc.lastIDs, []string{commentsTestID}) || svc.lastActor == "" {
			t.Errorf("%s: calls=%v ids=%v actor=%q", tok, svc.calls, svc.lastIDs, svc.lastActor)
		}
	}
}

func TestCommentApproveVouch(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	modPost(t, p, "owner-token", "/approve", url.Values{"vouch": {"1"}})
	if !slices.Equal(svc.calls, []string{"approve", "vouch"}) || !slices.Equal(svc.lastIDs, []string{"c1"}) {
		t.Errorf("calls=%v ids=%v", svc.calls, svc.lastIDs)
	}
}

func TestCommentTrash(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	modPost(t, p, "editor-token", "/trash", url.Values{})
	if !slices.Equal(svc.calls, []string{"trash"}) || !slices.Equal(svc.lastIDs, []string{commentsTestID}) {
		t.Errorf("calls=%v ids=%v", svc.calls, svc.lastIDs)
	}
}

func TestCommentSpamPage(t *testing.T) {
	t.Parallel()

	rows := modRows()
	rows[0].HasLinks = true
	p := newCommentsRig(t, &fakeComments{rows: rows})
	body := modGet(t, p, "owner-token", "/admin/comments/"+commentsTestID+"/spam")
	for _, want := range []string{`name="link_domains"`, `name="email_domain"`, "This blocks the commenter and remembers the text.", "Nice post"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, " disabled") {
		t.Error("the spam page ships a disabled control")
	}
}

func TestCommentSpamTeach(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	modPost(t, p, "owner-token", "/spam", url.Values{"link_domains": {"1"}})
	if !slices.Equal(svc.calls, []string{"spam"}) || !svc.lastTeach.LinkDomains || svc.lastTeach.EmailDomain {
		t.Errorf("calls=%v teach=%+v", svc.calls, svc.lastTeach)
	}
}

func TestCommentBackGuard(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows()})
	loc := modPost(t, p, "owner-token", "/trash", url.Values{"back": {"https://evil.example"}})
	if !strings.HasPrefix(loc, "/admin/comments") {
		t.Errorf("redirect = %q, want it under /admin/comments", loc)
	}
	loc = modPost(t, p, "owner-token", "/trash", url.Values{"back": {"/admin/comments?status=spam"}})
	if loc != "/admin/comments?status=spam" {
		t.Errorf("redirect = %q, want the page it came from", loc)
	}
}

func TestCommentNotFound(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{err: ErrCommentNotFound}
	p := newCommentsRig(t, svc)
	for _, path := range []string{"/approve", "/trash", "/spam"} {
		if rec := p.do("POST", "/admin/comments/"+commentsTestID+path, "owner-token", url.Values{}, nil); rec.Code != 404 {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := p.do("GET", "/admin/comments/"+commentsTestID+"/spam", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("GET spam = %d, want 404", rec.Code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
}

func TestCommentRowActionsPerTab(t *testing.T) {
	t.Parallel()

	rows := modRows()
	rows[0].Status = "spam"
	p := newCommentsRig(t, &fakeComments{rows: rows})
	body := modGet(t, p, "owner-token", "/admin/comments?status=spam")
	if !strings.Contains(body, "Not spam") {
		t.Error("spam tab lacks Not spam")
	}
	if strings.Contains(body, "Unapprove") || strings.Contains(body, "Approve + vouch") {
		t.Error("spam tab offers an action the model has no path for")
	}
	rows[0].Status = "trash"
	p = newCommentsRig(t, &fakeComments{rows: rows})
	if body := modGet(t, p, "owner-token", "/admin/comments?status=trash"); !strings.Contains(body, "Restore") {
		t.Error("trash tab lacks Restore")
	}
}

// modTooLongService refuses every reply like the formatter does for a long text.
type modTooLongService struct{ *fakeComments }

func (modTooLongService) Reply(context.Context, string, string, string, string) (string, error) {
	return "", ErrCommentTooLong
}

func modNoInput(t *testing.T, body string) {
	t.Helper()
	for _, bad := range []string{`name="url"`, `name="website"`, `name="author_url"`} {
		if strings.Contains(body, bad) {
			t.Errorf("page has an input %s", bad)
		}
	}
}

func TestCommentReply(t *testing.T) {
	t.Parallel()

	for _, tok := range []string{"owner-token", "editor-token"} {
		svc := &fakeComments{rows: modRows()}
		p := newCommentsRig(t, svc)
		body := modGet(t, p, tok, "/admin/comments/"+commentsTestID+"/reply")
		modNoInput(t, body)
		loc := modPost(t, p, tok, "/reply", url.Values{"body": {"  Thanks!  "}})
		if loc != "/admin/comments?status=approved" {
			t.Errorf("%s: redirect = %q", tok, loc)
		}
		if !slices.Equal(svc.calls, []string{"reply"}) || svc.lastActor == "" || svc.lastBody != "Thanks!" || !slices.Equal(svc.lastIDs, []string{commentsTestID}) {
			t.Errorf("%s: calls=%v actor=%q body=%q ids=%v", tok, svc.calls, svc.lastActor, svc.lastBody, svc.lastIDs)
		}
	}
}

func TestCommentReplyEmpty(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/comments/"+commentsTestID+"/reply", "owner-token", url.Values{"body": {"   "}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Write a reply.") {
		t.Errorf("empty reply = %d", rec.Code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
}

func TestCommentEdit(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	body := modGet(t, p, "owner-token", "/admin/comments/"+commentsTestID+"/edit")
	modNoInput(t, body)
	if !strings.Contains(body, "Nice post") {
		t.Error("the edit page does not prefill the text")
	}
	modPost(t, p, "editor-token", "/edit", url.Values{"body": {"Better text"}})
	if !slices.Equal(svc.calls, []string{"edit"}) || svc.lastBody != "Better text" {
		t.Errorf("calls=%v body=%q", svc.calls, svc.lastBody)
	}
}

func TestCommentEditTooLong(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/comments/"+commentsTestID+"/edit", "owner-token", url.Values{"body": {strings.Repeat("a", maxCommentBodyBytes+1)}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "This text is too long.") {
		t.Errorf("long edit = %d", rec.Code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
}

func TestCommentReplyServiceTooLong(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	p.a.deps.Comments = modTooLongService{svc}
	rec := p.do("POST", "/admin/comments/"+commentsTestID+"/reply", "owner-token", url.Values{"body": {"hello"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "This text is too long.") {
		t.Errorf("service too long = %d", rec.Code)
	}
}
