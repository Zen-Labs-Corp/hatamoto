//go:build integration

package comment

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type deletedRecorder struct {
	NopObserver
	ids []uuid.UUID
}

func (d *deletedRecorder) OnDeleted(_ context.Context, c Comment) { d.ids = append(d.ids, c.ID) }

func (d *deletedRecorder) has(id uuid.UUID) int {
	n := 0
	for _, x := range d.ids {
		if x == id {
			n++
		}
	}
	return n
}

func TestPurgeEventsSkipDead(t *testing.T) {
	f := newFixture(t)
	f.enable()
	spamA := f.seedAged(nil, StatusSpam, 31)
	spamB := f.seedAged(nil, StatusSpam, 31)
	trash := f.seedAged(nil, StatusTrash, 31)
	dead := f.seedAged(nil, StatusDead, 90)
	rec := &deletedRecorder{}
	f.svc.Register(rec)

	n, err := f.svc.PurgeSpamTrash(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(rec.ids) != 3 {
		t.Fatalf("purged %d, %d events, want 3 and 3", n, len(rec.ids))
	}
	for _, s := range []seeded{spamA, spamB, trash} {
		if rec.has(s.id) != 1 || f.exists(s.id) {
			t.Errorf("%s: events %d, exists %v", s.id, rec.has(s.id), f.exists(s.id))
		}
	}
	if !f.exists(dead.id) || rec.has(dead.id) != 0 {
		t.Error("a dead comment was purged")
	}
	var body string
	f.scalar(&body, `SELECT body_raw FROM hatamoto_comments WHERE id = $1`, dead.id)
	if body != "b" {
		t.Errorf("dead comment body blanked: %q", body)
	}
}

func TestDeleteCascadeEvents(t *testing.T) {
	f := newFixture(t)
	f.enable()
	root := f.seedComment(nil, f.svc.utcNow(), StatusApproved)
	r1 := f.seedComment(&root, f.svc.utcNow(), StatusApproved)
	r2 := f.seedComment(&r1, f.svc.utcNow(), StatusPending)
	f.seedComment(&root, f.svc.utcNow(), StatusDraft)
	other := f.seedComment(nil, f.svc.utcNow(), StatusApproved)
	rec := &deletedRecorder{}
	f.svc.Register(rec)

	if err := f.svc.Delete(f.ctx, root.id); err != nil {
		t.Fatal(err)
	}
	if len(rec.ids) != 3 {
		t.Fatalf("events = %d, want 3 (root and 2 replies, no draft)", len(rec.ids))
	}
	for _, s := range []seeded{root, r1, r2} {
		if rec.has(s.id) != 1 {
			t.Errorf("%s fired %d times", s.id, rec.has(s.id))
		}
	}
	if rec.has(other.id) != 0 {
		t.Error("an unrelated comment fired")
	}
}

func TestCollectForEntity(t *testing.T) {
	f := newFixture(t)
	f.enable()
	a := f.seedComment(nil, f.svc.utcNow(), StatusApproved)
	b := f.seedComment(&a, f.svc.utcNow(), StatusDead)
	f.seedComment(nil, f.svc.utcNow(), StatusDraft)
	rec := &deletedRecorder{}
	f.svc.Register(rec)

	announce, err := f.svc.CollectForEntity(f.ctx, f.db.Pool(), f.entity)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, f.entity)
	if len(rec.ids) != 0 {
		t.Fatal("events fired before announce")
	}
	announce(f.ctx)
	if len(rec.ids) != 2 || rec.has(a.id) != 1 || rec.has(b.id) != 1 {
		t.Errorf("events = %v, want one each for the two public comments", rec.ids)
	}
}
