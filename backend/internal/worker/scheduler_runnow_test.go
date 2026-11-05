package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRecorder struct {
	mu   sync.Mutex
	runs []Run
}

func (f *fakeRecorder) Record(_ context.Context, r Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, r)
	return nil
}

func (f *fakeRecorder) all() []Run {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Run(nil), f.runs...)
}

// startedScheduler returns a started scheduler with a recorder; every job is
// registered with a far-away first run, so only RunNow and runJob execute it.
func startedScheduler(t *testing.T) (*Scheduler, *fakeRecorder) {
	t.Helper()
	t.Setenv("WORKER_DRAIN", "")
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := &fakeRecorder{}
	s.SetRecorder(rec)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Stop() })
	return s, rec
}

func scheduleFar(t *testing.T, s *Scheduler, id string, fn func(context.Context) error) {
	t.Helper()
	if err := s.Schedule(id, time.Hour, fn); err != nil {
		t.Fatal(err)
	}
}

func TestRunNowRecordsSuccessAndFailure(t *testing.T) {
	s, rec := startedScheduler(t)
	scheduleFar(t, s, "prune", func(context.Context) error { return nil })
	scheduleFar(t, s, "changeset_sweep", func(context.Context) error { return errors.New("bad\nthing\x00") })

	r, err := s.RunNow(context.Background(), "prune")
	if err != nil || !r.OK || r.ID != "prune" {
		t.Fatalf("prune: %+v, %v", r, err)
	}
	r, err = s.RunNow(context.Background(), "changeset_sweep")
	if err != nil || r.OK || r.Err != "bad thing" {
		t.Fatalf("sweep: %+v, %v", r, err)
	}
	got := rec.all()
	if len(got) != 2 || !got[0].OK || got[1].OK || got[1].Err != "bad thing" {
		t.Fatalf("recorded %+v", got)
	}
}

func TestRunNowRefusals(t *testing.T) {
	s, _ := startedScheduler(t)
	scheduleFar(t, s, "plugin:p:j", func(context.Context) error { return nil })
	scheduleFar(t, s, "stats_ping", func(context.Context) error { return nil })
	for id, want := range map[string]error{
		"plugin:p:j": ErrNotSafe,
		"stats_ping": ErrNotSafe,
		"nope":       ErrUnknownJob,
		"prune":      ErrNotRunning, // safe, but not registered on this process
	} {
		if _, err := s.RunNow(context.Background(), id); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", id, err, want)
		}
	}
}

func TestRunNowNotStartedAndDrain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewScheduler(nil, logger)
	scheduleFar(t, s, "prune", func(context.Context) error { return nil })
	if _, err := s.RunNow(context.Background(), "prune"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("not started: %v", err)
	}
	if s.Running() {
		t.Fatal("Running before Start")
	}

	t.Setenv("WORKER_DRAIN", "true")
	s2 := NewScheduler(nil, logger)
	scheduleFar(t, s2, "prune", func(context.Context) error { return nil })
	s2.Start(t.Context())
	if _, err := s2.RunNow(context.Background(), "prune"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("drain: %v", err)
	}
	if s2.Running() {
		t.Fatal("Running in drain mode")
	}
}

func TestRunNowOverlapWithTicker(t *testing.T) {
	s, _ := startedScheduler(t)
	entered, release := make(chan struct{}), make(chan struct{})
	scheduleFar(t, s, "prune", func(context.Context) error {
		close(entered)
		<-release
		return nil
	})
	// The ticker path: runJob holds the per-job lock.
	st := s.state("prune")
	go s.runJob(context.Background(), "prune", st.fn)
	<-entered
	if jobs := s.Jobs(); len(jobs) != 1 || !jobs[0].Running {
		t.Fatalf("Jobs = %+v", jobs)
	}
	if _, err := s.RunNow(context.Background(), "prune"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	close(release)
}

func TestRunNowConcurrentNeverOverlaps(t *testing.T) {
	s, rec := startedScheduler(t)
	var in, maxIn, calls atomic.Int32
	scheduleFar(t, s, "prune", func(context.Context) error {
		n := in.Add(1)
		for {
			m := maxIn.Load()
			if n <= m || maxIn.CompareAndSwap(m, n) {
				break
			}
		}
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		in.Add(-1)
		return nil
	})
	var wg sync.WaitGroup
	var ok, busy atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := s.RunNow(context.Background(), "prune"); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrAlreadyRunning):
				busy.Add(1)
			default:
				t.Errorf("err = %v", err)
			}
		}()
	}
	wg.Wait()
	if maxIn.Load() != 1 || ok.Load() < 1 || ok.Load()+busy.Load() != 8 || int(calls.Load()) != int(ok.Load()) || len(rec.all()) != int(ok.Load()) {
		t.Fatalf("max in flight %d, ok %d, busy %d, calls %d, recorded %d", maxIn.Load(), ok.Load(), busy.Load(), calls.Load(), len(rec.all()))
	}
}

func TestRunNowUsesManualVariantAndOutlivesCaller(t *testing.T) {
	s, rec := startedScheduler(t)
	var manual atomic.Bool
	release := make(chan struct{})
	scheduleFar(t, s, "update_check", func(context.Context) error { t.Error("scheduled fn must not run"); return nil })
	s.SetManual("update_check", func(context.Context) error {
		manual.Store(true)
		<-release
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.RunNow(ctx, "update_check"); !errors.Is(err, ErrStillRunning) {
		t.Fatalf("err = %v, want ErrStillRunning", err)
	}
	close(release)
	deadline := time.After(2 * time.Second)
	for len(rec.all()) == 0 {
		select {
		case <-deadline:
			t.Fatal("run was not recorded after the caller left")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !manual.Load() {
		t.Fatal("manual variant not used")
	}
}

func TestFixedJobsUseCanonicalIDs(t *testing.T) {
	t.Setenv("WORKER_DRAIN", "")
	s := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetPruneWorker(&PruneWorker{})
	s.SetRetentionPurgeWorker(&RetentionPurgeWorker{})
	s.SetEntityVersionWorker(&EntityVersionWorker{})
	s.Start(t.Context())
	t.Cleanup(s.Stop)
	var ids []string
	for _, j := range s.Jobs() {
		ids = append(ids, j.ID)
	}
	want := []string{"entity_versions", "prune", "retention_purge"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestRunNowManualOnlyRefusesWithoutManualVariant(t *testing.T) {
	s, rec := startedScheduler(t)
	scheduleFar(t, s, "update_check", func(context.Context) error { t.Error("scheduled fn must not run"); return nil })
	if _, err := s.RunNow(context.Background(), "update_check"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d runs recorded, want none", n)
	}
}
