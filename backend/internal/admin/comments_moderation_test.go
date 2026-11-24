package admin

import (
	"context"
	"fmt"
	"net/http/httptest"
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
	for _, want := range []string{`<p class="hm-muted">Nice post</p>`, `<p class="hm-muted">Held: contains a link, first comment</p>`, ">new<", "0 approved"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Nice post · Held") {
		t.Error("excerpt and reason share a line")
	}
}

func TestCommentsListDeadTab(t *testing.T) {
	t.Parallel()

	row := modRows()[0]
	row.Status, row.DeadBy, row.Reasons = "dead", "example", nil
	p := newCommentsRig(t, &fakeComments{rows: []CommentRow{row}, counts: CommentCounts{Pending: 1, Dead: 3}})
	body := modGet(t, p, "owner-token", "/admin/comments?status=dead")
	for _, want := range []string{`comments-seg-dead-n`, ">Dead<", "Marked dead by plugin example.", "Restore", "Trash", "Delete"} {
		if !strings.Contains(body, want) {
			t.Errorf("dead tab lacks %q", want)
		}
	}
	for _, id := range []string{"pending", "approved", "spam", "trash", "dead"} {
		if !strings.Contains(body, `comments-seg-`+id+`-n`) {
			t.Errorf("segment %s has no count", id)
		}
	}
	if !strings.Contains(modGet(t, p, "owner-token", "/admin/comments?status=bogus"), "1 waiting for review") {
		t.Error("a bogus status does not fall back to pending")
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

func TestCommentsListCommenterFilter(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	body := modGet(t, p, "owner-token", "/admin/comments?status=approved&commenter="+cmtrTestID)
	if svc.lastFilter.CommenterID != cmtrTestID || svc.lastFilter.Status != "approved" {
		t.Errorf("filter = %+v, want the commenter and status passed to the queue", svc.lastFilter)
	}
	if !strings.Contains(body, "One commenter") || strings.Contains(body, `comments-seg-approved-n`) {
		t.Error("a commenter-filtered view has no chip, or shows site-wide tab counts")
	}
	modGet(t, p, "owner-token", "/admin/comments?commenter=not-a-uuid")
	if svc.lastFilter.CommenterID != "" {
		t.Errorf("a malformed commenter id reached the queue: %q", svc.lastFilter.CommenterID)
	}
}

func TestCommentsListFormKeepsCommenter(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{rows: modRows()})
	body := modGet(t, p, "owner-token", "/admin/comments?status=approved&commenter="+cmtrTestID)
	want := `<input type="hidden" name="commenter" value="` + cmtrTestID + `">`
	if !strings.Contains(body, want) {
		t.Error("the filter form (status tabs, search, selects) does not carry commenter")
	}
	if !strings.Contains(body, "commenter="+cmtrTestID) {
		t.Error("the chip/clear links lost commenter")
	}
	if body := modGet(t, p, "owner-token", "/admin/comments"); strings.Contains(body, `name="commenter"`) {
		t.Error("an unfiltered list carries a commenter field")
	}
}

func TestCommentsListPageClamped(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	modGet(t, p, "owner-token", "/admin/comments?page=9223372036854775807")
	if svc.lastFilter.Page != maxListPage {
		t.Errorf("queue page = %d, want it clamped to %d", svc.lastFilter.Page, maxListPage)
	}
	if got := pageParam(httptest.NewRequest("GET", "/x?page=9223372036854775807", nil)); got != maxListPage {
		t.Errorf("pageParam = %d, want %d", got, maxListPage)
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

// Every single-comment moderation action leaves an audit event with the actor and the comment id.
func TestCommentModerationAudited(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ path, action string }{
		{"/approve", "comments.approve"}, {"/trash", "comments.trash"}, {"/spam", "comments.spam"},
		{"/reply", "comments.reply"}, {"/edit", "comments.edit"},
	} {
		p := newCommentsRig(t, &fakeComments{rows: modRows()})
		modPost(t, p, "editor-token", tc.path, url.Values{"body": {"Some text"}})
		if len(p.events) != 1 || p.events[0].Action != tc.action || p.events[0].Target != commentsTestID || p.events[0].Actor != "ed@example.com" || p.events[0].ActorID == "" {
			t.Errorf("%s: audit events = %+v", tc.path, p.events)
		}
	}
	p := newCommentsRig(t, &fakeComments{rows: modRows()})
	modPost(t, p, "owner-token", "/approve", url.Values{"vouch": {"1"}})
	if len(p.events) != 2 || p.events[0].Action != "comments.approve" || p.events[1].Action != "comments.commenter.vouch" {
		t.Errorf("approve+vouch audit events = %+v", p.events)
	}
}

func TestCommentApproveVouchKeepsBlock(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{rows: modRows(), blocked: true}
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/comments/"+commentsTestID+"/approve", "owner-token", url.Values{"vouch": {"1"}}, nil)
	if rec.Code != 303 || !slices.Equal(svc.calls, []string{"approve"}) {
		t.Fatalf("code=%d calls=%v, want the approval only", rec.Code, svc.calls)
	}
	if flash := p.followFlash(rec.Result().Cookies()); !strings.Contains(flash, "blocked, so they were not vouched for") {
		t.Errorf("flash = %q", flash)
	}
	// The commenter list's own Vouch refuses a blocked commenter too.
	rec = p.do("POST", "/admin/settings/comments/commenters/"+cmtrTestID+"/vouch", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || len(svc.calls) != 1 || !strings.Contains(p.followFlash(rec.Result().Cookies()), "cannot be vouched for") {
		t.Errorf("commenter vouch: code=%d calls=%v", rec.Code, svc.calls)
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
	for _, want := range []string{`name="link_domains"`, `name="email_domain"`, "This blocks the commenter and remembers the text.", "Nice post", "Only for throwaway domains", "such as gmail.com"} {
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

func TestCommentReplyNeverPublishesEmail(t *testing.T) {
	t.Parallel()

	// The editor has no display name, so CurrentUser.Name is the login email.
	svc := &fakeComments{rows: modRows()}
	p := newCommentsRig(t, svc)
	modPost(t, p, "editor-token", "/reply", url.Values{"body": {"Thanks!"}})
	if svc.lastAuthor != "Acme Site" {
		t.Errorf("author = %q, want the site name, never the email", svc.lastAuthor)
	}
	svc = &fakeComments{rows: modRows()}
	p = newCommentsRig(t, svc)
	modPost(t, p, "owner-token", "/reply", url.Values{"body": {"Thanks!"}})
	if svc.lastAuthor != "Owner Olga" {
		t.Errorf("author = %q, want the display name", svc.lastAuthor)
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

const modSecondID = "7d2a1b3c-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

func modBulk(p *perfRig, token string, form url.Values, hdr map[string]string) (int, string) {
	rec := p.do("POST", "/admin/comments/bulk", token, form, hdr)
	return rec.Code, rec.Body.String()
}

func TestCommentsBulkApprove(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	code, _ := modBulk(p, "editor-token", url.Values{"action": {"approve"}, "id": {commentsTestID, modSecondID, commentsTestID}}, nil)
	if code != 303 || !slices.Equal(svc.calls, []string{"approve"}) || !slices.Equal(svc.lastIDs, []string{commentsTestID, modSecondID}) {
		t.Errorf("code=%d calls=%v ids=%v", code, svc.calls, svc.lastIDs)
	}
}

func TestCommentsBulkPartial(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{skip: 1}
	p := newCommentsRig(t, svc)
	rec := p.do("POST", "/admin/comments/bulk", "owner-token", url.Values{"action": {"trash"}, "id": {commentsTestID, modSecondID}}, nil)
	if rec.Code != 303 {
		t.Fatalf("code = %d", rec.Code)
	}
	if flash := p.followFlash(rec.Result().Cookies()); !strings.Contains(flash, "1 comment moved to trash.") || !strings.Contains(flash, "1 skipped") {
		t.Errorf("flash = %q, want done and skipped counts", flash)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.trash" || p.events[0].Detail["done"] != 1 || p.events[0].Detail["skipped"] != 1 {
		t.Errorf("audit events = %+v", p.events)
	}
	// Nothing could change: say so, and audit nothing.
	svc.skip = 2
	p.events = nil
	rec = p.do("POST", "/admin/comments/bulk", "owner-token", url.Values{"action": {"trash"}, "id": {commentsTestID, modSecondID}}, nil)
	if flash := p.followFlash(rec.Result().Cookies()); !strings.Contains(flash, "Nothing changed") || len(p.events) != 0 {
		t.Errorf("flash = %q events = %+v", flash, p.events)
	}
}

func TestCommentsBulkActionsPerTab(t *testing.T) {
	t.Parallel()

	for tab, hidden := range map[string]string{modStatusApproved: "Approve selected", modStatusSpam: "Approve selected", modStatusTrash: "Approve selected"} {
		for _, a := range modBulkActions("/admin", tab) {
			if a.Label == hidden {
				t.Errorf("%s tab offers %q", tab, a.Label)
			}
		}
	}
	has := func(tab, label string) bool {
		for _, a := range modBulkActions("/admin", tab) {
			if a.Label == label {
				return true
			}
		}
		return false
	}
	if has(modStatusSpam, "Spam") || has(modStatusTrash, "Trash") || !has(modStatusPending, "Approve selected") || !has(modStatusTrash, "Restore") {
		t.Error("bulk actions do not follow the tab")
	}
}

func TestCommentsBulkSpam(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	code, _ := modBulk(p, "owner-token", url.Values{"action": {"spam"}, "id": {commentsTestID}}, nil)
	if code != 303 || !slices.Equal(svc.calls, []string{"spam"}) || svc.lastTeach != (SpamTeach{}) {
		t.Errorf("code=%d calls=%v teach=%+v", code, svc.calls, svc.lastTeach)
	}
}

func TestCommentsBulkDeleteNeedsConfirm(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	code, body := modBulk(p, "owner-token", url.Values{"action": {"delete"}, "id": {commentsTestID, modSecondID}}, nil)
	if code != 200 || !strings.Contains(body, "and their replies") || !strings.Contains(body, `name="confirmed"`) {
		t.Errorf("unconfirmed delete = %d", code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
	code, _ = modBulk(p, "owner-token", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {commentsTestID, modSecondID}}, nil)
	if code != 303 || !slices.Equal(svc.calls, []string{"delete"}) {
		t.Errorf("confirmed delete = %d calls=%v", code, svc.calls)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.delete" || p.events[0].Detail["done"] != 2 {
		t.Errorf("audit events = %+v", p.events)
	}
}

func TestCommentsBulkCap(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	form := url.Values{"action": {"trash"}}
	for i := range 101 {
		form.Add("id", fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	code, _ := modBulk(p, "owner-token", form, nil)
	if code != 303 || len(svc.calls) != 0 {
		t.Errorf("code=%d calls=%v", code, svc.calls)
	}
	code, _ = modBulk(p, "owner-token", url.Values{"action": {"trash"}}, nil)
	if code != 303 || len(svc.calls) != 0 {
		t.Errorf("no ids: code=%d calls=%v", code, svc.calls)
	}
}

func TestCommentsBulkBadAction(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	if code, _ := modBulk(p, "owner-token", url.Values{"action": {"nuke"}, "id": {commentsTestID}}, nil); code != 400 {
		t.Errorf("bad action = %d, want 400", code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
}

func TestCommentsBulkNoCSRF(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	if code, _ := modBulk(p, "owner-token", url.Values{"action": {"trash"}, "id": {commentsTestID}}, map[string]string{"X-CSRF-Token": ""}); code != 403 {
		t.Errorf("no CSRF = %d, want 403", code)
	}
	if len(svc.calls) != 0 {
		t.Errorf("calls = %v", svc.calls)
	}
}

func init() {
	areaPages["commentsmoderation"] = []string{"comments/list", "comments/spam", "comments/reply", "comments/edit"}
}

func TestHeldReasonLabels(t *testing.T) {
	t.Parallel()

	got := HeldReasonLabels([]string{"contains a link", "new commenter", "x"})
	if want := []string{"contains a link", "first comment", "x"}; !slices.Equal(got, want) {
		t.Errorf("HeldReasonLabels = %v, want %v", got, want)
	}
}
