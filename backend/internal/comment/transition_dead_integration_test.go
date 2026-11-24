//go:build integration

package comment

import (
	"context"
	"testing"
	"time"
)

type eventRecorder struct {
	NopObserver
	approved, deleted int
	updated           [][2]Status
}

func (r *eventRecorder) OnApproved(context.Context, Comment) { r.approved++ }
func (r *eventRecorder) OnDeleted(context.Context, Comment)  { r.deleted++ }
func (r *eventRecorder) OnUpdated(_ context.Context, before, after Comment) {
	r.updated = append(r.updated, [2]Status{before.Status, after.Status})
}

func TestTransitionDead(t *testing.T) {
	f := newFixture(t)
	f.enable()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := f.seedComment(nil, base, StatusDead)
	rec := &eventRecorder{}
	f.svc.Register(rec)

	got, err := f.svc.Approve(f.ctx, seeded.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved {
		t.Fatalf("status = %s, want approved", got.Status)
	}
	if rec.approved != 0 {
		t.Errorf("OnApproved fired %d times on a restore, want 0", rec.approved)
	}
	if len(rec.updated) != 1 || rec.updated[0] != [2]Status{StatusDead, StatusApproved} {
		t.Errorf("OnUpdated = %v, want one dead -> approved", rec.updated)
	}
	f.wantCounts(1, 0, "after restore")

	rec.updated = nil
	if _, err := f.svc.transition(f.ctx, seeded.id, StatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.updated) != 1 || rec.updated[0] != [2]Status{StatusApproved, StatusDead} {
		t.Errorf("OnUpdated = %v, want one approved -> dead", rec.updated)
	}
	f.wantCounts(0, 0, "approved -> dead")

	if _, err := f.svc.MarkSpam(f.ctx, seeded.id, nil, Learn{}); err != nil {
		t.Fatalf("dead -> spam: %v", err)
	}
	if got := f.statusOf(seeded.id); got != StatusSpam {
		t.Fatalf("status after MarkSpam = %s, want spam", got)
	}
	if _, err := f.svc.transition(f.ctx, seeded.id, StatusApproved, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.transition(f.ctx, seeded.id, StatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Trash(f.ctx, seeded.id); err != nil {
		t.Fatal(err)
	}
	if rec.deleted != 1 {
		t.Errorf("OnDeleted fired %d times on trashing a dead comment, want 1", rec.deleted)
	}
}
