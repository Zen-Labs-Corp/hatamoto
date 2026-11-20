package admin

import (
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
