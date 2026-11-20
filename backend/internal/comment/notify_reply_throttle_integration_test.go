//go:build integration

package comment

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const window = 15 * time.Minute

// parentBy stores an approved, opted-in, confirmed parent by the given address.
func (f *fixture) parentBy(email string) Comment {
	f.t.Helper()
	d := f.draft(email, "question")
	d.Status, d.NotifyReplies = StatusApproved, true
	return f.store(d)
}

// queueAt stores a reply to parent by a new replier and queues it at the given time.
func (f *fixture) queueAt(parent Comment, at time.Time) Comment {
	f.t.Helper()
	f.clock(at)
	r := f.replyTo(parent, fmt.Sprintf("replier%s@example.test", uuid.NewString()[:8]))
	f.queueReply(r)
	return r
}

func (f *fixture) batches(at time.Time) []ReplyBatch {
	f.t.Helper()
	f.clock(at)
	got, err := f.svc.ClaimReplyBatches(f.ctx, 100, window)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func replyIDs(b ReplyBatch) []uuid.UUID {
	ids := make([]uuid.UUID, len(b.Replies))
	for i, r := range b.Replies {
		ids[i] = r.ID
	}
	return ids
}

func sameIDs(t *testing.T, label string, got []uuid.UUID, want ...Comment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d replies, want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i].ID {
			t.Fatalf("%s: reply %d is %s, want %s (queued order)", label, i, got[i], want[i].ID)
		}
	}
}

// wantOne fails unless got is exactly one batch and returns it.
func wantOne(t *testing.T, label string, got []ReplyBatch) ReplyBatch {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%s: %d batches, want 1", label, len(got))
	}
	return got[0]
}

func wantNone(t *testing.T, label string, got []ReplyBatch) {
	t.Helper()
	if len(got) != 0 {
		t.Fatalf("%s: %d batches, want 0", label, len(got))
	}
}

func TestReplyThrottleImmediateThenDigest(t *testing.T) {
	f := newFixture(t)
	parent := f.parentBy("parent@example.test")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	var replies []Comment
	for i := 0; i < 5; i++ {
		replies = append(replies, f.queueAt(parent, t0.Add(time.Duration(i)*30*time.Second)))
	}

	s := t0.Add(2 * time.Minute)
	b := wantOne(t, "first claim", f.batches(s))
	sameIDs(t, "first claim", replyIDs(b), replies[0])
	if b.Email != "parent@example.test" || b.CommenterID != *parent.CommenterID {
		t.Errorf("batch recipient = %s %s", b.CommenterID, b.Email)
	}
	wantNone(t, "S+1m", f.batches(s.Add(time.Minute)))
	wantNone(t, "S+14m59s", f.batches(s.Add(window-time.Second)))
	b = wantOne(t, "S+15m", f.batches(s.Add(window)))
	sameIDs(t, "digest", replyIDs(b), replies[1:]...)
	wantNone(t, "after digest", f.batches(s.Add(window)))

	// A reply inside the window the digest opened is held, then goes alone.
	late := f.queueAt(parent, s.Add(20*time.Minute))
	wantNone(t, "held reply", f.batches(s.Add(20*time.Minute)))
	b = wantOne(t, "held reply after window", f.batches(s.Add(30*time.Minute)))
	sameIDs(t, "held reply", replyIDs(b), late)

	// After a full quiet window a new reply is claimed at once.
	quiet := f.queueAt(parent, s.Add(time.Hour))
	b = wantOne(t, "quiet then reply", f.batches(s.Add(time.Hour)))
	sameIDs(t, "quiet then reply", replyIDs(b), quiet)
}

func TestReplyThrottlePerRecipient(t *testing.T) {
	f := newFixture(t)
	a, b := f.parentBy("a@example.test"), f.parentBy("b@example.test")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	a1 := f.queueAt(a, t0)
	got := wantOne(t, "A first", f.batches(t0))
	sameIDs(t, "A first", replyIDs(got), a1)

	f.queueAt(a, t0.Add(30*time.Second)) // held
	b1 := f.queueAt(b, t0.Add(time.Minute))
	got = wantOne(t, "B first", f.batches(t0.Add(time.Minute)))
	if got.CommenterID != *b.CommenterID {
		t.Fatalf("batch for %s, want B", got.CommenterID)
	}
	sameIDs(t, "B first", replyIDs(got), b1)
	wantNone(t, "A still held", f.batches(t0.Add(2*time.Minute)))
}

func TestReplyThrottleUnsubscribedErasedBlocked(t *testing.T) {
	cases := []struct {
		name   string
		change func(f *fixture, parent Comment)
		unsent bool // held rows must be deleted, not left unsent
	}{
		{"unsubscribed", func(f *fixture, p Comment) {
			if _, err := f.svc.UnsubscribeReplies(f.ctx, *p.CommenterID); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"blocked", func(f *fixture, p Comment) {
			f.exec(`UPDATE hatamoto_commenters SET trust_level = 'blocked' WHERE id = $1`, *p.CommenterID)
		}, true},
		{"erased", func(f *fixture, p Comment) {
			if _, err := f.svc.EraseByEmail(f.ctx, "held@example.test"); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			parent := f.parentBy("held@example.test")
			t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
			f.queueAt(parent, t0)
			wantOne(t, "first", f.batches(t0))
			var held []uuid.UUID
			for i := 1; i <= 3; i++ {
				held = append(held, f.queueAt(parent, t0.Add(time.Duration(i)*time.Minute)).ID)
			}
			tc.change(f, parent)
			wantNone(t, "after window", f.batches(t0.Add(16*time.Minute)))
			var n int
			if tc.unsent {
				f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_reply_queue WHERE comment_id = ANY($1)`, held)
			} else {
				f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_reply_queue WHERE comment_id = ANY($1) AND sent_at IS NOT NULL`, held)
			}
			if n != 0 {
				t.Errorf("%d held rows left (deleted for unsubscribe/blocked, never stamped for erase), want 0", n)
			}
		})
	}
}

func TestReplyThrottleDroppedRowsDoNotCount(t *testing.T) {
	f := newFixture(t)
	parent := f.parentBy("parent@example.test")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f.clock(t0)
	self := f.replyTo(parent, "parent@example.test") // the parent's own reply
	f.queueReply(self)
	wantNone(t, "self-reply", f.batches(t0))
	if f.replyQueued(self) != 0 {
		t.Error("self-reply row kept, want deleted")
	}

	genuine := f.queueAt(parent, t0.Add(time.Minute))
	b := wantOne(t, "genuine reply", f.batches(t0.Add(time.Minute)))
	sameIDs(t, "genuine reply", replyIDs(b), genuine)
}

func TestReplyThrottleConcurrentClaims(t *testing.T) {
	f := newFixture(t)
	a, b := f.parentBy("a@example.test"), f.parentBy("b@example.test")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	var all []uuid.UUID
	for i := 0; i < 5; i++ {
		all = append(all, f.queueAt(a, t0).ID)
	}
	for i := 0; i < 2; i++ {
		all = append(all, f.queueAt(b, t0).ID)
	}

	round := func(at time.Time) map[uuid.UUID][][]uuid.UUID {
		f.clock(at) // before the goroutines start: no race on svc.now
		var mu sync.Mutex
		per := map[uuid.UUID][][]uuid.UUID{}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := f.svc.ClaimReplyBatches(f.ctx, 100, window)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, bt := range got {
					per[bt.CommenterID] = append(per[bt.CommenterID], replyIDs(bt))
				}
			}()
		}
		wg.Wait()
		return per
	}
	check := func(label string, per map[uuid.UUID][][]uuid.UUID, wantA, wantB int) []uuid.UUID {
		var seen []uuid.UUID
		for who, n := range map[uuid.UUID]int{*a.CommenterID: wantA, *b.CommenterID: wantB} {
			if len(per[who]) != 1 || len(per[who][0]) != n {
				t.Fatalf("%s: recipient %s got batches %v, want exactly one with %d replies", label, who, per[who], n)
			}
			seen = append(seen, per[who][0]...)
		}
		return seen
	}
	seen := check("t0", round(t0), 1, 1)
	seen = append(seen, check("t0+16m", round(t0.Add(16*time.Minute)), 4, 1)...)

	byString := func(ids []uuid.UUID) {
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	}
	byString(seen)
	byString(all)
	if len(seen) != len(all) {
		t.Fatalf("claimed %d replies in total, want %d each once: %v", len(seen), len(all), seen)
	}
	for i := range all {
		if seen[i] != all[i] {
			t.Fatalf("claimed set differs: got %v want %v", seen, all)
		}
	}
}
