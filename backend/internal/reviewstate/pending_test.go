package reviewstate

import (
	"context"
	"errors"
	"testing"
)

func TestPendingUnavailableWithoutSource(t *testing.T) {
	resetGroups(t)
	if _, err := Pending(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v; want ErrUnavailable", err)
	}
}

func TestPendingPassesRowsAndLimit(t *testing.T) {
	resetGroups(t)
	var gotLimit int
	SetPendingSource(func(_ context.Context, limit int) ([]PendingStage, error) {
		gotLimit = limit
		return []PendingStage{{ChangesetID: 7, Index: 1, Total: 2}}, nil
	})
	rows, err := Pending(context.Background())
	if err != nil || len(rows) != 1 || rows[0].ChangesetID != 7 || gotLimit != MaxPending {
		t.Fatalf("got %#v, %v, limit %d", rows, err, gotLimit)
	}
}

func TestResetClearsPendingSource(t *testing.T) {
	resetGroups(t)
	SetPendingSource(func(context.Context, int) ([]PendingStage, error) { return nil, nil })
	Reset()
	if _, err := Pending(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v; want ErrUnavailable", err)
	}
}

func TestSetPendingSourceNilPanics(t *testing.T) {
	resetGroups(t)
	mustPanic(t, "nil source", func() { SetPendingSource(nil) })
}
