package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
)

// scriptedPoll drives a poller through a fixed list of tick results with a
// fake clock that advances one tick per call, so coalescing is deterministic.
type scriptedPoll struct {
	steps  []pollStep
	clock  time.Time
	purges []int // call index of each purge
	calls  int
}

// pollStep is one tick: whether a new change is seen, or a read error.
type pollStep struct {
	changed bool
	err     error
}

const scriptedEvery = time.Second

// run feeds the steps to a poller with the given gap and returns once all are
// consumed. purges holds the (0-based) tick index of each purge.
func (sp *scriptedPoll) run(t *testing.T, gap time.Duration) {
	t.Helper()
	sp.clock = time.Unix(1000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &contentEpochPoller{
		changed: func(context.Context) (bool, error) {
			if sp.calls >= len(sp.steps) {
				cancel()
				return false, nil
			}
			st := sp.steps[sp.calls]
			sp.calls++
			sp.clock = sp.clock.Add(scriptedEvery)
			return st.changed, st.err
		},
		onChange: func(context.Context) { sp.purges = append(sp.purges, sp.calls-1) },
		every:    time.Millisecond,
		gap:      gap,
		now:      func() time.Time { return sp.clock },
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not consume the script")
	}
}

func expectPurges(t *testing.T, sp *scriptedPoll, want ...int) {
	t.Helper()
	if len(sp.purges) != len(want) {
		t.Fatalf("purges at ticks %v, want %v", sp.purges, want)
	}
	for i := range want {
		if sp.purges[i] != want[i] {
			t.Fatalf("purges at ticks %v, want %v", sp.purges, want)
		}
	}
}

var (
	idle   = pollStep{}
	change = pollStep{changed: true}
	fail   = pollStep{err: errors.New("db down")}
)

func TestContentEpochPoller_IdleNeverPurges(t *testing.T) {
	sp := &scriptedPoll{steps: []pollStep{idle, idle, idle, idle, idle, idle}}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp)
}

func TestContentEpochPoller_OneChangeOnePurge(t *testing.T) {
	// Tick 0 only seeds; a change at tick 3 purges once, at once.
	sp := &scriptedPoll{steps: []pollStep{idle, idle, idle, change, idle, idle, idle}}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp, 3)
}

func TestContentEpochPoller_BurstIsCoalescedWithTrailingPurge(t *testing.T) {
	// A change every tick for 12 ticks with a 5s gap: a leading purge at tick 1,
	// then at most one per gap, then a trailing one on the first quiet tick.
	steps := []pollStep{idle}
	for range 12 {
		steps = append(steps, change)
	}
	steps = append(steps, idle, idle, idle)
	sp := &scriptedPoll{steps: steps}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp, 1, 6, 11, 13)
}

func TestContentEpochPoller_ChangeInsideGapPurgesOnQuietTick(t *testing.T) {
	sp := &scriptedPoll{steps: []pollStep{idle, change, change, idle, idle}}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp, 1, 3)
}

func TestContentEpochPoller_ErrorNeverPurgesAndRecovers(t *testing.T) {
	// Seeded, then reads fail, then recover with nothing new: no purge. A
	// change after that purges once.
	sp := &scriptedPoll{steps: []pollStep{idle, fail, fail, idle, idle, change, idle}}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp, 5)
}

func TestContentEpochPoller_FailedSeedPurgesOnFirstSuccess(t *testing.T) {
	// Reads failed at boot: a change may have landed unseen, so the first
	// success purges once, and only once.
	sp := &scriptedPoll{steps: []pollStep{fail, fail, idle, idle, idle}}
	sp.run(t, 5*time.Second)
	expectPurges(t, sp, 2)
}

func TestContentEpochPoller_MissingTableDisablesIt(t *testing.T) {
	calls := 0
	p := &contentEpochPoller{
		changed:  func(context.Context) (bool, error) { calls++; return false, errEpochUnavailable },
		onChange: func(context.Context) { t.Error("purged") },
		every:    5 * time.Millisecond,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	done := make(chan struct{})
	go func() { defer close(done); p.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller kept running after the change log was reported missing")
	}
	if calls != 1 {
		t.Errorf("reads = %d, want 1", calls)
	}
}

func TestContentEpochPoller_StopsOnCancel(t *testing.T) {
	p := &contentEpochPoller{
		changed:  func(context.Context) (bool, error) { return false, nil },
		onChange: func(context.Context) {},
		every:    5 * time.Millisecond,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not exit after the context was cancelled")
	}
}

func TestContentEpochEvery(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"production":  3 * time.Second,
		"development": time.Second,
		"":            time.Second,
	} {
		s := &Server{cfg: &config.Config{Environment: env}}
		if got := s.contentEpochEvery(); got != want {
			t.Errorf("environment %q: every = %v, want %v", env, got, want)
		}
	}
}

func TestContentEpochChangeOrder(t *testing.T) {
	var got []string
	step := func(name string) func() { return func() { got = append(got, name) } }
	rendered := func(context.Context) { got = append(got, "rendered") }

	contentEpochChange(context.Background(), step("config"), step("urls"), rendered)
	if want := "config,urls,rendered"; strings.Join(got, ",") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}

	got = nil
	contentEpochChange(context.Background(), nil, nil, rendered)
	if len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("nil steps: got %v, want only rendered", got)
	}
}

func TestOnContentEpochChangeNilSafe(t *testing.T) {
	s := &Server{}
	s.pageCache = nil
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("partial server panicked: %v", r)
		}
	}()
	s.onContentEpochChange(context.Background())
}
