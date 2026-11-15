package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"hatamoto.dev/core/internal/offsite"
)

func TestOffsiteNextDue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	slot := now.Add(6 * time.Hour)
	store := &memOffsiteStore{}
	o := &offsiteService{store: store, now: func() time.Time { return now }}
	ctx := context.Background()

	if _, ok := o.nextDue(ctx); ok {
		t.Fatal("a disabled backup has no due time: the job may idle")
	}

	store.set = offsite.Settings{Enabled: true, Schedule: offsite.Schedule{Kind: offsite.ScheduleOff}, NextDueAt: slot}
	if _, ok := o.nextDue(ctx); ok {
		t.Fatal("schedule off has no due time")
	}

	store.set.Schedule.Kind = offsite.ScheduleDaily
	if got, ok := o.nextDue(ctx); !ok || !got.Equal(slot) {
		t.Fatalf("nextDue = %v, %v; want the slot %v", got, ok, slot)
	}

	store.set.NextDueAt = time.Time{} // saved, not yet slotted
	if got, ok := o.nextDue(ctx); !ok || !got.Equal(now) {
		t.Fatalf("unslotted nextDue = %v, %v; want now", got, ok)
	}
}

type failingOffsiteStore struct{ *memOffsiteStore }

func (*failingOffsiteStore) Get(context.Context) (offsite.Settings, bool, error) {
	return offsite.Settings{}, false, errors.New("db down")
}

func TestOffsiteNextDueStoreErrorIsDueNow(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	o := &offsiteService{store: &failingOffsiteStore{&memOffsiteStore{}}, now: func() time.Time { return now }}
	if got, ok := o.nextDue(context.Background()); !ok || !got.Equal(now) {
		t.Fatalf("nextDue = %v, %v; want now so the gate keeps the normal interval", got, ok)
	}
}
