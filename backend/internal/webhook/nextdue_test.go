package webhook

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNextDueQueryErrorIsDueNow(t *testing.T) {
	due := time.Now().Add(time.Hour)
	r := &Repo{minDue: func(context.Context) (*time.Time, error) { return &due, nil }}
	if got, ok := r.NextDue(context.Background()); !ok || !got.Equal(due) {
		t.Fatalf("NextDue = %v, %v; want %v", got, ok, due)
	}
	r.minDue = func(context.Context) (*time.Time, error) { return nil, nil }
	if _, ok := r.NextDue(context.Background()); ok {
		t.Fatal("an empty queue has nothing due")
	}
	r.minDue = func(context.Context) (*time.Time, error) { return nil, errors.New("db down") }
	before := time.Now()
	if got, ok := r.NextDue(context.Background()); !ok || got.Before(before) {
		t.Fatalf("a failed query = %v, %v; want due now so the gate keeps the normal interval", got, ok)
	}
}
