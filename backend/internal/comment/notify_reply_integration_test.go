//go:build integration

package comment

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// optedInParent stores an approved, opted-in, confirmed parent by parent@example.test.
func (f *fixture) optedInParent() Comment {
	f.t.Helper()
	d := f.draft("parent@example.test", "question")
	d.Status, d.NotifyReplies = StatusApproved, true
	return f.store(d)
}

func (f *fixture) replyTo(parent Comment, email string) Comment {
	f.t.Helper()
	d := f.draft(email, "answer")
	d.ParentID, d.Status = &parent.ID, StatusApproved
	return f.store(d)
}

func (f *fixture) replyQueued(reply Comment) int {
	f.t.Helper()
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_reply_queue WHERE comment_id = $1`, reply.ID)
	return n
}

func (f *fixture) claimBatches() []ReplyBatch {
	f.t.Helper()
	got, err := f.svc.ClaimReplyBatches(f.ctx, 100, 15*time.Minute)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// claimReplies returns the claimed replies of all batches.
func (f *fixture) claimReplies() []Comment {
	f.t.Helper()
	var out []Comment
	for _, b := range f.claimBatches() {
		out = append(out, b.Replies...)
	}
	return out
}

func (f *fixture) queueReply(reply Comment) {
	f.t.Helper()
	if err := f.svc.QueueReply(f.ctx, reply); err != nil {
		f.t.Fatal(err)
	}
}

func TestReplyQueueSendsToConfirmedOptedInParent(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	reply := f.replyTo(parent, "other@example.test")
	f.queueReply(reply)
	f.queueReply(reply) // once per reply
	if f.replyQueued(reply) != 1 {
		t.Fatalf("queue rows = %d, want 1", f.replyQueued(reply))
	}
	got := f.claimBatches()
	if len(got) != 1 || len(got[0].Replies) != 1 {
		t.Fatalf("claimed %d batches, want 1 with 1 reply", len(got))
	}
	b := got[0]
	if b.Email != "parent@example.test" || b.Replies[0].ID != reply.ID || b.CommenterID != *parent.CommenterID {
		t.Errorf("batch = %+v", b)
	}
	if again := f.claimReplies(); len(again) != 0 {
		t.Errorf("second claim = %d, want 0", len(again))
	}
	f.queueReply(reply) // a re-approval never mails again
	if again := f.claimReplies(); len(again) != 0 {
		t.Errorf("claim after re-queue = %d, want 0", len(again))
	}
}

func TestReplyQueueRequiresOptInAndConfirmation(t *testing.T) {
	f := newFixture(t)

	plain := f.store(func() Draft {
		d := f.draft("plain@example.test", "no mail please")
		d.Status = StatusApproved
		return d
	}())
	notOpted := f.replyTo(plain, "other@example.test")
	f.queueReply(notOpted)

	// A draft a moderator approved was never confirmed by its author.
	d := f.draft("unconfirmed@example.test", "held")
	d.NotifyReplies = true
	draft := f.store(d)
	if _, err := f.svc.Approve(f.ctx, draft.ID, nil); err != nil {
		t.Fatal(err)
	}
	var confirmed *time.Time
	f.scalar(&confirmed, `SELECT confirmed_at FROM hatamoto_comments WHERE id = $1`, draft.ID)
	if confirmed != nil {
		t.Fatalf("approving a draft set confirmed_at = %v; test premise broken", confirmed)
	}
	unconfirmed := f.replyTo(draft, "other@example.test")
	f.queueReply(unconfirmed)

	// A top-level comment has no parent to mail.
	f.queueReply(plain)

	for name, r := range map[string]Comment{"not opted in": notOpted, "unconfirmed": unconfirmed, "top level": plain} {
		if f.replyQueued(r) != 0 {
			t.Errorf("%s: reply was queued", name)
		}
	}
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("claimed %d, want 0", len(got))
	}
}

func TestReplyQueueNeverToSelf(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	own := f.replyTo(parent, "parent@example.test")
	f.queueReply(own)
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("claimed %d replies to the replier themself, want 0", len(got))
	}
	// The row is dropped, never left to retry and never counted as a send.
	if f.replyQueued(own) != 0 {
		t.Error("self reply stayed queued")
	}
}

func TestReplyQueueUnsubscribe(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	reply := f.replyTo(parent, "other@example.test")
	f.queueReply(reply)
	n, err := f.svc.UnsubscribeReplies(f.ctx, *parent.CommenterID)
	if err != nil || n != 1 {
		t.Fatalf("unsubscribe = %d, %v, want 1", n, err)
	}
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("claimed %d after unsubscribe, want 0", len(got))
	}
	if n, err := f.svc.UnsubscribeReplies(f.ctx, *parent.CommenterID); err != nil || n != 0 {
		t.Errorf("second unsubscribe = %d, %v, want 0", n, err)
	}
	if n, err := f.svc.UnsubscribeReplies(f.ctx, uuid.New()); err != nil || n != 0 {
		t.Errorf("unknown commenter = %d, %v, want a no-op", n, err)
	}
	// Ticking the box on a later comment opts in again.
	d := f.draft("parent@example.test", "again")
	d.Status, d.NotifyReplies = StatusApproved, true
	again := f.store(d)
	r2 := f.replyTo(again, "other@example.test")
	f.queueReply(r2)
	if got := f.claimReplies(); len(got) != 1 {
		t.Errorf("claimed %d after opting in again, want 1", len(got))
	}
}

func TestReplyQueueBlockedAndErasedCommenter(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	reply := f.replyTo(parent, "other@example.test")
	f.queueReply(reply)
	f.exec(`UPDATE hatamoto_commenters SET trust_level = 'blocked' WHERE id = $1`, *parent.CommenterID)
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("blocked commenter: claimed %d, want 0", len(got))
	}

	erased := f.store(func() Draft {
		d := f.draft("gone@example.test", "bye")
		d.Status, d.NotifyReplies = StatusApproved, true
		return d
	}())
	r2 := f.replyTo(erased, "other@example.test")
	f.queueReply(r2)
	if _, err := f.svc.EraseByEmail(f.ctx, "gone@example.test"); err != nil {
		t.Fatal(err)
	}
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("erased commenter: claimed %d, want 0", len(got))
	}
}

func TestReplyQueueUnapprovedReplyIsDropped(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	reply := f.replyTo(parent, "other@example.test")
	f.queueReply(reply)
	if _, err := f.svc.Trash(f.ctx, reply.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.claimReplies(); len(got) != 0 {
		t.Errorf("claimed %d for a trashed reply, want 0", len(got))
	}
}

func TestPruneReplyQueue(t *testing.T) {
	f := newFixture(t)
	parent := f.optedInParent()
	var rows [4]Comment
	for i := range rows {
		rows[i] = f.replyTo(parent, "o"+uuid.NewString()[:6]+"@example.test")
		f.queueReply(rows[i])
	}
	f.exec(`UPDATE hatamoto_comment_reply_queue SET queued_at = now() - interval '8 days' WHERE comment_id = $1`, rows[0].ID)
	f.exec(`UPDATE hatamoto_comment_reply_queue SET queued_at = now() - interval '2 days' WHERE comment_id = $1`, rows[1].ID)
	f.exec(`UPDATE hatamoto_comment_reply_queue SET sent_at = now() - interval '31 days' WHERE comment_id = $1`, rows[2].ID)
	f.exec(`UPDATE hatamoto_comment_reply_queue SET sent_at = now() - interval '5 days' WHERE comment_id = $1`, rows[3].ID)
	n, err := f.svc.PruneReplyQueue(f.ctx)
	if err != nil || n != 2 {
		t.Fatalf("pruned = %d, %v, want 2", n, err)
	}
	want := [4]int{0, 1, 0, 1}
	for i, r := range rows {
		if got := f.replyQueued(r); got != want[i] {
			t.Errorf("row %d: present = %d, want %d", i, got, want[i])
		}
	}
}
