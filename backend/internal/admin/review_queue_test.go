package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

func (f *fakeReviewFlow) Queue(context.Context, int, int) ([]PendingEdit, int, error) {
	return f.queue, len(f.queue), nil
}

func (f *fakeReviewFlow) Count(context.Context) (int, error) { return len(f.queue), nil }

func (f *fakeReviewFlow) Detail(_ context.Context, id int64) (ReviewDetail, error) {
	d, ok := f.details[id]
	if !ok {
		return ReviewDetail{}, ErrReviewNotFound
	}
	return d, nil
}

func (f *fakeReviewFlow) act(name string, id int64, comment string) error {
	f.calls = append(f.calls, name+" "+strconv.FormatInt(id, 10)+" "+comment)
	return f.failure
}

func (f *fakeReviewFlow) Approve(_ context.Context, id int64, c string) (ReviewOutcome, error) {
	return f.outcome, f.act("approve", id, c)
}
func (f *fakeReviewFlow) SendBack(_ context.Context, id int64, c string) error {
	return f.act("send-back", id, c)
}
func (f *fakeReviewFlow) Publish(_ context.Context, id int64) (ReviewOutcome, error) {
	return f.outcome, f.act("publish", id, "")
}
func (f *fakeReviewFlow) Withdraw(_ context.Context, id int64) error {
	return f.act("withdraw", id, "")
}
func (f *fakeReviewFlow) Discard(_ context.Context, id int64) error { return f.act("discard", id, "") }

// editQueueRig is a roles rig with one change (id 7) by the author waiting on a published entry.
type editQueueRig struct {
	*contentRig
	rv     *fakeReviewFlow
	users  map[user.Role]*user.User
	author *user.User
	live   *entity.Entity
}

func newEditQueueRig(t *testing.T) *editQueueRig {
	t.Helper()
	rv := &fakeReviewFlow{}
	c, users := newRolesRig(t, func(d *Deps) { d.Review = rv })
	author := users[user.RoleAuthor]
	live := c.entityBy(author, entity.StatusPublished)
	payload := *live
	payload.Name, payload.Slug = "New title", live.Slug
	payload.Attributes = []byte(`{"summary":"A better summary"}`)
	pe := PendingEdit{ID: 7, EntityID: live.ID.String(), EntityName: "New title", Requester: author.ID.String(),
		RequesterName: "Author Sam", Since: time.Now().Add(-2 * time.Hour), Payload: &payload}
	rv.queue = []PendingEdit{pe}
	rv.details = map[int64]ReviewDetail{7: {PendingEdit: pe, Live: live}}
	return &editQueueRig{contentRig: c, rv: rv, users: users, author: author, live: live}
}

func (r *editQueueRig) post(t *testing.T, who *user.User, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	tok := "owner-token"
	if who != nil {
		tok = r.freshToken(who.ID)
	}
	return r.do("POST", "/admin/review/7/"+path, tok, form, nil)
}

func TestReviewQueue(t *testing.T) {
	r := newEditQueueRig(t)
	editor := r.users[user.RoleReviewer]
	body := r.get(t, r.freshToken(editor.ID), "/admin/review", "")
	has(t, body, "New title", "Author Sam", "2h ago", "/admin/review/7")

	r.rv.queue = nil
	has(t, r.get(t, "owner-token", "/admin/review", ""), "Nothing is waiting for review.")

	for _, role := range []user.Role{user.RoleAuthor, user.RoleContributor, user.RoleReadOnly} {
		if rec := r.do("GET", "/admin/review", r.freshToken(r.users[role].ID), nil, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s GET /review = %d, want 403", role, rec.Code)
		}
	}
	r2 := newContentRig(t)
	if rec := r2.do("GET", "/admin/review", "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no review flow: %d, want 503", rec.Code)
	}
}

func TestReviewDetail(t *testing.T) {
	r := newEditQueueRig(t)

	t.Run("a decider sees the diff and the decision form", func(t *testing.T) {
		body := r.get(t, "owner-token", "/admin/review/7", "")
		has(t, body, "Author Sam asked to change this entry", `<code>name</code>`, `<code>summary</code>`, "A better summary",
			"Approve and publish", "Send back", `name="comment"`, "/admin/review/7/approve", "/admin/review/7/send-back", "/admin/review/7/discard")
		lacks(t, body, "/admin/review/7/withdraw")
	})
	t.Run("the requester sees no decision form, only Withdraw", func(t *testing.T) {
		body := r.get(t, r.freshToken(r.author.ID), "/admin/review/7", "")
		has(t, body, "/admin/review/7/withdraw", "New title")
		lacks(t, body, "Approve and publish", `name="comment"`, "/admin/review/7/approve", "/admin/review/7/discard")
	})
	t.Run("anyone else gets the page of a change that is not there", func(t *testing.T) {
		for _, role := range []user.Role{user.RoleContributor, user.RoleReadOnly} {
			if rec := r.do("GET", "/admin/review/7", r.freshToken(r.users[role].ID), nil, nil); rec.Code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404", role, rec.Code)
			}
		}
		if rec := r.do("GET", "/admin/review/8", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("unknown id = %d, want 404", rec.Code)
		}
	})
	t.Run("a stale change offers Send back only", func(t *testing.T) {
		d := r.rv.details[7]
		d.Stale = true
		r.rv.details[7] = d
		body := r.get(t, "owner-token", "/admin/review/7", "")
		has(t, body, "This entry changed after it was sent for review. Send it back so it can be redone.", "Send back")
		lacks(t, body, "Approve and publish")
		d.Stale = false
		r.rv.details[7] = d
	})
	t.Run("a stage with an unknown group cannot be decided", func(t *testing.T) {
		d := r.rv.details[7]
		d.NeedsGroup = "legal"
		r.rv.details[7] = d
		body := r.get(t, "owner-token", "/admin/review/7", "")
		has(t, body, "This stage needs the group legal, which this site does not define. An owner can discard the change.")
		lacks(t, body, "Approve and publish")
		d.NeedsGroup = ""
		r.rv.details[7] = d
	})
	t.Run("a sent-back change shows the comment", func(t *testing.T) {
		d := r.rv.details[7]
		d.Rejected, d.Comment, d.Reviewer = true, "Fix the intro.", "Alex"
		r.rv.details[7] = d
		has(t, r.get(t, "owner-token", "/admin/review/7", ""), "Sent back by Alex: Fix the intro.")
	})
}

func TestReviewApproveChecksTheDecideRight(t *testing.T) {
	r := newEditQueueRig(t)
	for _, action := range []string{"approve", "send-back", "publish", "discard"} {
		for _, role := range []user.Role{user.RoleAuthor, user.RoleContributor, user.RoleReadOnly} {
			rec := r.post(t, r.users[role], action, url.Values{"comment": {"x"}})
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s by %s = %d, want 403", action, role, rec.Code)
			}
		}
	}
	if len(r.rv.calls) != 0 {
		t.Fatalf("a person without the decide right reached the service: %v", r.rv.calls)
	}

	rec := r.post(t, r.users[user.RoleReviewer], "approve", url.Values{"comment": {"Looks good"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/review" {
		t.Fatalf("approve = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	r.rv.outcome = ReviewOutcome{Published: true}
	has(t, r.next(rec, "/admin/review"), "Approved. The change is live.")
	if len(r.rv.calls) != 1 || r.rv.calls[0] != "approve 7 Looks good" {
		t.Errorf("calls = %v", r.rv.calls)
	}
}

func TestReviewRefusesSelfApproval(t *testing.T) {
	r := newEditQueueRig(t)
	// The owner sent this change (a policy staged it): the owner may not decide it.
	d := r.rv.details[7]
	d.Requester = r.owner.ID.String()
	r.rv.details[7] = d
	for _, action := range []string{"approve", "send-back", "publish"} {
		rec := r.post(t, nil, action, url.Values{"comment": {"x"}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "You cannot approve your own change.") {
			t.Errorf("%s by the requester = %d", action, rec.Code)
		}
	}
	if len(r.rv.calls) != 0 {
		t.Errorf("a self decision reached the service: %v", r.rv.calls)
	}
	// The service refusing it as well maps to the same answer.
	d.Requester = r.author.ID.String()
	r.rv.details[7] = d
	r.rv.failure = ErrReviewSelf
	if rec := r.post(t, nil, "approve", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("service self refusal = %d", rec.Code)
	}
}

func TestReviewStaleApprovalIsRefused(t *testing.T) {
	r := newEditQueueRig(t)
	r.rv.failure = ErrReviewStale
	rec := r.post(t, nil, "approve", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("approve of a stale change = %d, want 409", rec.Code)
	}
	has(t, rec.Body.String(), "This entry changed after it was sent for review. Send it back so it can be redone.")
	lacks(t, rec.Body.String(), "Approve and publish")
}

func TestReviewSendBackNeedsAComment(t *testing.T) {
	r := newEditQueueRig(t)
	rec := r.post(t, nil, "send-back", url.Values{"comment": {"  "}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty comment = %d, want 422", rec.Code)
	}
	has(t, rec.Body.String(), "Add a comment so the author knows what to change.")
	if len(r.rv.calls) != 0 {
		t.Fatalf("an empty send back reached the service: %v", r.rv.calls)
	}
	rec = r.post(t, nil, "send-back", url.Values{"comment": {"Fix the intro."}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("send back = %d", rec.Code)
	}
	has(t, r.next(rec, "/admin/review"), "Sent back. The author has been told.")
	if len(r.rv.calls) != 1 || r.rv.calls[0] != "send-back 7 Fix the intro." {
		t.Errorf("calls = %v", r.rv.calls)
	}
}

func TestReviewWithdrawAndDiscard(t *testing.T) {
	r := newEditQueueRig(t)
	// Only the requester withdraws; a decider who did not send it discards.
	if rec := r.post(t, r.users[user.RoleReviewer], "withdraw", nil); rec.Code != http.StatusForbidden {
		t.Errorf("withdraw by a decider = %d, want 403", rec.Code)
	}
	if rec := r.post(t, r.users[user.RoleContributor], "withdraw", nil); rec.Code != http.StatusNotFound {
		t.Errorf("withdraw by a stranger = %d, want 404", rec.Code)
	}
	if rec := r.post(t, r.author, "discard", nil); rec.Code != http.StatusForbidden {
		t.Errorf("discard by the requester = %d, want 403", rec.Code)
	}
	if len(r.rv.calls) != 0 {
		t.Fatalf("a refused request reached the service: %v", r.rv.calls)
	}
	rec := r.post(t, r.author, "withdraw", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/entities/"+r.live.ID.String() {
		t.Fatalf("withdraw = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := r.post(t, r.users[user.RoleReviewer], "discard", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("discard by a decider = %d", rec.Code)
	}
	if len(r.rv.calls) != 2 || r.rv.calls[0] != "withdraw 7 " || r.rv.calls[1] != "discard 7 " {
		t.Errorf("calls = %v", r.rv.calls)
	}
}
