package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestOnce(t *testing.T) {
	ran := 0
	fn := func(context.Context) error { ran++; return nil }
	claimWith := func(ok bool, err error) func(context.Context, string, time.Duration) (bool, error) {
		return func(context.Context, string, time.Duration) (bool, error) { return ok, err }
	}

	if err := Once(claimWith(false, nil), "id", time.Minute, fn)(context.Background()); !errors.Is(err, ErrNotClaimed) || ran != 0 {
		t.Fatalf("lost claim: err %v, ran %d", err, ran)
	}
	boom := errors.New("db down")
	if err := Once(claimWith(false, boom), "id", time.Minute, fn)(context.Background()); !errors.Is(err, boom) || ran != 0 {
		t.Fatalf("claim error: err %v, ran %d", err, ran)
	}
	if err := Once(claimWith(true, nil), "id", time.Minute, fn)(context.Background()); err != nil || ran != 1 {
		t.Fatalf("won claim: err %v, ran %d", err, ran)
	}
}

func TestOncePassesIDAndWindow(t *testing.T) {
	var gotID string
	var gotWin time.Duration
	claim := func(_ context.Context, id string, w time.Duration) (bool, error) {
		gotID, gotWin = id, w
		return true, nil
	}
	_ = Once(claim, "module:a:b", 90*time.Second, func(context.Context) error { return nil })(context.Background())
	if gotID != "module:a:b" || gotWin != 90*time.Second {
		t.Fatalf("claim got %q %s", gotID, gotWin)
	}
}

func TestLostClaimIsSilent(t *testing.T) {
	var logs bytes.Buffer
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(&logs, nil)))
	rec := &fakeRecorder{}
	s.SetRecorder(rec)
	st := &jobState{}
	r := s.execute(context.Background(), st, "module:a:b", func(context.Context) error { return ErrNotClaimed })
	if len(rec.all()) != 0 {
		t.Fatalf("recorded %+v", rec.all())
	}
	if logs.Len() != 0 {
		t.Fatalf("logged %q", logs.String())
	}
	if r.ID != "module:a:b" || !r.Started.IsZero() {
		t.Fatalf("run = %+v", r)
	}
}
