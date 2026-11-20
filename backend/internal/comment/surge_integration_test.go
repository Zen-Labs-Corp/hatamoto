//go:build integration

package comment

import (
	"sync"
	"testing"
	"time"
)

func TestNoteSurgeEpisodes(t *testing.T) {
	f := newFixture(t)
	quiet := 10 * time.Minute
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	step := []struct {
		at   time.Duration
		want bool
	}{
		{0, true},
		{1 * time.Minute, false},
		{9 * time.Minute, false},
		{18 * time.Minute, false}, // each beat keeps the episode open
		{28 * time.Minute, true},  // exactly quiet after the last beat
		{28 * time.Minute, false},
	}
	for _, s := range step {
		f.clock(t0.Add(s.at))
		got, err := f.svc.NoteSurge(f.ctx, quiet)
		if err != nil {
			t.Fatal(err)
		}
		if got != s.want {
			t.Fatalf("NoteSurge at +%s = %v, want %v", s.at, got, s.want)
		}
	}

	// A replica with an older clock neither wins nor moves the beat back.
	f.clock(t0.Add(20 * time.Minute))
	got, err := f.svc.NoteSurge(f.ctx, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("an older clock must not start a new episode")
	}
	var through time.Time
	f.scalar(&through, `SELECT sent_through FROM hatamoto_digest_state WHERE name = $1`, surgeStateName)
	if want := t0.Add(28 * time.Minute); !through.Equal(want) {
		t.Errorf("sent_through = %s, want %s (not moved back)", through, want)
	}
}

func TestNoteSurgeConcurrentOneWinner(t *testing.T) {
	f := newFixture(t)
	quiet := 10 * time.Minute
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	round := func(name string) {
		var mu sync.Mutex
		var wins int
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				won, err := f.svc.NoteSurge(f.ctx, quiet)
				if err != nil {
					t.Error(err)
					return
				}
				if won {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%s: %d winners, want exactly 1", name, wins)
		}
	}

	f.clock(t0)
	round("no row yet")
	f.clock(t0.Add(11 * time.Minute))
	round("stale row")
}
