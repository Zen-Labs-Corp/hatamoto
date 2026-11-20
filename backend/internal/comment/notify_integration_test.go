//go:build integration

package comment

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *fixture) user(email string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO hatamoto_users (id, email, role, name) VALUES ($1, $2, 'editor', 'U')`, id, email)
	return id
}

func (f *fixture) pending() Comment {
	f.t.Helper()
	d := f.draft("held"+uuid.NewString()[:8]+"@example.test", "held")
	d.Status = StatusPending
	return f.store(d)
}

func (f *fixture) queueLen(user uuid.UUID) int {
	f.t.Helper()
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_notify_queue WHERE user_id = $1`, user)
	return n
}

func (f *fixture) clock(t time.Time) { f.svc.now = func() time.Time { return t } }

func TestModerationQueueAndThrottle(t *testing.T) {
	f := newFixture(t)
	a, b := f.user("a@example.test"), f.user("b@example.test")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f.clock(t0)

	var held []Comment
	for i := 0; i < 3; i++ {
		c := f.pending()
		held = append(held, c)
		if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{a, b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.QueueModeration(f.ctx, held[0].ID, []uuid.UUID{a, b}); err != nil {
		t.Fatal(err) // idempotent
	}
	if f.queueLen(a) != 3 {
		t.Fatalf("queue for a = %d, want 3", f.queueLen(a))
	}

	batches, err := f.svc.ClaimModeration(f.ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	for _, b := range batches {
		if len(b.Comments) != 3 {
			t.Errorf("user %s got %d comments, want 3", b.UserID, len(b.Comments))
		}
	}

	// Throttled: a new held comment waits for the gap.
	later := f.pending()
	if err := f.svc.QueueModeration(f.ctx, later.ID, []uuid.UUID{a, b}); err != nil {
		t.Fatal(err)
	}
	f.clock(t0.Add(9 * time.Minute))
	if got, err := f.svc.ClaimModeration(f.ctx, 10*time.Minute); err != nil || len(got) != 0 {
		t.Fatalf("claim inside the gap = %v, %v, want none", got, err)
	}
	f.clock(t0.Add(10 * time.Minute))
	got, err := f.svc.ClaimModeration(f.ctx, 10*time.Minute)
	if err != nil || len(got) != 2 {
		t.Fatalf("claim after the gap = %d batches, %v, want 2", len(got), err)
	}
	for _, b := range got {
		if len(b.Comments) != 1 || b.Comments[0].ID != later.ID {
			t.Errorf("user %s batch = %+v, want the later comment", b.UserID, b.Comments)
		}
	}
	if got, err := f.svc.ClaimModeration(f.ctx, 10*time.Minute); err != nil || len(got) != 0 {
		t.Errorf("immediate re-claim = %v, %v, want none", got, err)
	}
}

func TestModerationClaimSkipsModeratedComments(t *testing.T) {
	f := newFixture(t)
	u := f.user("m@example.test")
	approved, still := f.pending(), f.pending()
	for _, c := range []Comment{approved, still} {
		if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{u}); err != nil {
			t.Fatal(err)
		}
	}
	f.enable()
	if _, err := f.svc.Approve(f.ctx, approved.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ClaimModeration(f.ctx, time.Minute)
	if err != nil || len(got) != 1 || len(got[0].Comments) != 1 || got[0].Comments[0].ID != still.ID {
		t.Fatalf("claim = %+v, %v, want only the still pending comment", got, err)
	}
}

func TestModerationMuted(t *testing.T) {
	f := newFixture(t)
	u, other := f.user("mute@example.test"), f.user("keep@example.test")
	c := f.pending()
	if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{u, other}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetModeratorMuted(f.ctx, u, true); err != nil {
		t.Fatal(err)
	}
	if f.queueLen(u) != 0 || f.queueLen(other) != 1 {
		t.Errorf("after mute: queue u=%d other=%d, want 0 and 1", f.queueLen(u), f.queueLen(other))
	}
	if err := f.svc.QueueModeration(f.ctx, f.pending().ID, []uuid.UUID{u}); err != nil {
		t.Fatal(err)
	}
	if f.queueLen(u) != 0 {
		t.Error("muted moderator got a queue row")
	}
	if err := f.svc.SetModeratorMuted(f.ctx, u, false); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{u}); err != nil {
		t.Fatal(err)
	}
	if f.queueLen(u) != 1 {
		t.Error("unmuted moderator was not queued")
	}
	// Unknown users are a no-op everywhere.
	ghost := uuid.New()
	if err := f.svc.SetModeratorMuted(f.ctx, ghost, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{ghost}); err != nil {
		t.Fatal(err)
	}
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_moderator_notify WHERE user_id = $1`, ghost)
	if n != 0 {
		t.Error("unknown user got a state row")
	}
}

func TestModerationClear(t *testing.T) {
	f := newFixture(t)
	u := f.user("clear@example.test")
	if err := f.svc.QueueModeration(f.ctx, f.pending().ID, []uuid.UUID{u}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ClearModeration(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.queueLen(u) != 0 {
		t.Error("queue not cleared")
	}
}

func TestModerationConcurrentClaims(t *testing.T) {
	f := newFixture(t)
	users := []uuid.UUID{f.user("c1@example.test"), f.user("c2@example.test")}
	var want []uuid.UUID
	for i := 0; i < 4; i++ {
		c := f.pending()
		want = append(want, c.ID)
		if err := f.svc.QueueModeration(f.ctx, c.ID, users[:1]); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var seen []uuid.UUID
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batches, err := f.svc.ClaimModeration(f.ctx, 10*time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, b := range batches {
				for _, c := range b.Comments {
					seen = append(seen, c.ID)
				}
			}
		}()
	}
	wg.Wait()
	byString := func(ids []uuid.UUID) {
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	}
	byString(seen)
	byString(want)
	if len(seen) != len(want) {
		t.Fatalf("claimed %d comments in total, want %d each once: %v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("claimed set differs: got %v want %v", seen, want)
		}
	}
}

func TestPruneModerationQueue(t *testing.T) {
	f := newFixture(t)
	u := f.user("a@example.test")
	held, approved := f.pending(), f.pending()
	for _, c := range []Comment{held, approved} {
		if err := f.svc.QueueModeration(f.ctx, c.ID, []uuid.UUID{u}); err != nil {
			t.Fatal(err)
		}
	}
	f.exec(`UPDATE hatamoto_comments SET status = 'approved' WHERE id = $1`, approved.ID)
	n, err := f.svc.PruneModerationQueue(f.ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned = %d, %v, want 1", n, err)
	}
	if f.queueLen(u) != 1 {
		t.Errorf("queue = %d, want the still-pending row only", f.queueLen(u))
	}
}
