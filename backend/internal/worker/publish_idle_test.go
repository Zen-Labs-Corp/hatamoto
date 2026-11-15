package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fakeMinDue(t *time.Time, err error) func(context.Context) (*time.Time, error) {
	return func(context.Context) (*time.Time, error) { return t, err }
}

func TestPublishNextDue(t *testing.T) {
	due := time.Now().Add(90 * time.Second)
	w := &PublishWorker{minDue: fakeMinDue(&due, nil)}
	if got, ok := w.NextDue(context.Background()); !ok || !got.Equal(due) {
		t.Fatalf("NextDue = %v, %v; want %v", got, ok, due)
	}
	w.minDue = fakeMinDue(nil, nil)
	if _, ok := w.NextDue(context.Background()); ok {
		t.Fatal("nothing scheduled must report no due time")
	}
	w.minDue = fakeMinDue(&due, errors.New("db down"))
	if _, ok := w.NextDue(context.Background()); ok {
		t.Fatal("a failed query must fall back to the beat")
	}
}

func TestPublishNextDueEarliestOfPublishAndUnpublish(t *testing.T) {
	pub, unpub := time.Now().Add(10*time.Minute), time.Now().Add(2*time.Minute)
	s := &Scheduler{
		publishWorker:   &PublishWorker{minDue: fakeMinDue(&pub, nil)},
		unpublishWorker: &UnpublishWorker{minDue: fakeMinDue(&unpub, nil)},
	}
	if got, ok := s.publishNextDue(context.Background()); !ok || !got.Equal(unpub) {
		t.Fatalf("got %v, %v; want the unpublish time %v", got, ok, unpub)
	}
	s.unpublishWorker = nil
	if got, ok := s.publishNextDue(context.Background()); !ok || !got.Equal(pub) {
		t.Fatalf("got %v, %v; want the publish time %v", got, ok, pub)
	}
}

// A post that comes due while idle runs at its due time, not at the beat.
func TestIdlePublishWakesAtDueNotBeat(t *testing.T) {
	r := newIdleRig(t)
	due := r.clk.now().Add(3*time.Minute + 90*time.Second)
	r.s.publishWorker = &PublishWorker{minDue: fakeMinDue(&due, nil)}
	r.s.OnIdle("publish", r.s.publishNextDue)
	r.startEvery("publish", time.Minute)
	<-r.ran
	<-r.asked
	r.clk.advance(3 * time.Minute)
	r.fire <- time.Time{}
	<-r.ran
	if d := <-r.asked; d != 90*time.Second {
		t.Fatalf("idle wait = %s, want 90s (due time, the beat is 2m away)", d)
	}
	r.fire <- time.Time{}
	<-r.ran
}
