package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type fakeSource struct {
	mu       sync.Mutex
	ids      []uuid.UUID // sorted
	calls    map[uuid.UUID]int
	fail     map[uuid.UUID]error
	onRegen  func(n int) // called after the n-th regenerate, with the mutex free
	regens   int
	nameByID map[uuid.UUID]string
}

func newFakeSource(n int) *fakeSource {
	f := &fakeSource{calls: map[uuid.UUID]int{}, fail: map[uuid.UUID]error{}, nameByID: map[uuid.UUID]string{}}
	for range n {
		f.ids = append(f.ids, uuid.New())
	}
	sort.Slice(f.ids, func(i, j int) bool { return f.ids[i].String() < f.ids[j].String() })
	return f
}

func (f *fakeSource) CountImages(context.Context) (int, error) { return len(f.ids), nil }

func (f *fakeSource) ListImagesAfter(_ context.Context, after uuid.UUID, limit int) ([]*Media, error) {
	var out []*Media
	for _, id := range f.ids {
		if id.String() > after.String() && len(out) < limit {
			out = append(out, &Media{ID: id, Filename: f.nameByID[id]})
		}
	}
	return out, nil
}

func (f *fakeSource) RegenerateVariants(_ context.Context, id uuid.UUID) (*Media, error) {
	f.mu.Lock()
	f.calls[id]++
	f.regens++
	n := f.regens
	err := f.fail[id]
	f.mu.Unlock()
	if f.onRegen != nil {
		f.onRegen(n)
	}
	return &Media{ID: id}, err
}

type memState struct {
	mu sync.Mutex
	st State
}

func (m *memState) Load(context.Context) (Settings, State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Settings{WebP: true, AVIF: true}, m.st, nil
}

func (m *memState) SaveState(_ context.Context, st State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st = st
	return nil
}

type hookLog struct {
	mu      sync.Mutex
	actions []string
	done    int
}

func (h *hookLog) hooks() RebuildHooks {
	return RebuildHooks{
		Audit: func(_ context.Context, action, _ string, _ map[string]any) {
			h.mu.Lock()
			h.actions = append(h.actions, action)
			h.mu.Unlock()
		},
		Done: func(context.Context) { h.mu.Lock(); h.done++; h.mu.Unlock() },
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRebuildRunsEveryImageOnce(t *testing.T) {
	ctx := context.Background()
	src, store, log := newFakeSource(45), &memState{}, &hookLog{}
	r := NewRebuilder(src, store, func() context.Context { return ctx }, log.hooks(), quiet())
	if err := r.Start(ctx, "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	r.Wait(ctx)
	st, _ := r.Status(ctx)
	if st.Running || st.Done != 45 || st.Total != 45 || st.Last == nil || st.Last.Images != 45 {
		t.Fatalf("state %+v", st)
	}
	for id, n := range src.calls {
		if n != 1 {
			t.Errorf("%s processed %d times", id, n)
		}
	}
	if len(src.calls) != 45 || log.done != 1 {
		t.Errorf("calls %d, done hook %d", len(src.calls), log.done)
	}
	if len(log.actions) != 2 || log.actions[0] != "media.rebuild.start" || log.actions[1] != "media.rebuild.finish" {
		t.Errorf("audit %v", log.actions)
	}
}

func TestRebuildRefusesASecondRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, store := newFakeSource(3), &memState{}
	block := make(chan struct{})
	src.onRegen = func(int) { <-block }
	r := NewRebuilder(src, store, func() context.Context { return ctx }, RebuildHooks{}, quiet())
	if err := r.Start(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, "b"); !errors.Is(err, ErrRebuildRunning) {
		t.Errorf("second start: %v", err)
	}
	close(block)
	r.Wait(ctx)
	if err := r.Start(ctx, "c"); err != nil {
		t.Errorf("start after the run ended: %v", err)
	}
	r.Wait(ctx)
}

func TestRebuildResumesFromTheCursor(t *testing.T) {
	src, store, log := newFakeSource(30), &memState{}, &hookLog{}
	// First server: cancelled after 7 images.
	ctx1, cancel1 := context.WithCancel(context.Background())
	src.onRegen = func(n int) {
		if n == 7 {
			cancel1()
		}
	}
	r1 := NewRebuilder(src, store, func() context.Context { return ctx1 }, log.hooks(), quiet())
	if err := r1.Start(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	r1.Wait(context.Background())
	st, _ := r1.Status(context.Background())
	if !st.Running || st.Done != 6 && st.Done != 7 {
		t.Fatalf("an interrupted run must stay running: %+v", st)
	}
	if st.Cursor != src.ids[st.Done-1] {
		t.Errorf("cursor %s after %d done", st.Cursor, st.Done)
	}

	// Second server: Resume ends the run.
	src.onRegen = nil
	ctx2 := context.Background()
	r2 := NewRebuilder(src, store, func() context.Context { return ctx2 }, log.hooks(), quiet())
	if err := r2.Start(ctx2, "owner"); !errors.Is(err, ErrRebuildRunning) {
		t.Errorf("start over an unfinished run: %v", err)
	}
	r2.Resume(ctx2)
	r2.Wait(ctx2)
	st, _ = r2.Status(ctx2)
	if st.Running || st.Done != 30 || st.Total != 30 {
		t.Fatalf("after resume: %+v", st)
	}
	for id, n := range src.calls {
		if n > 2 {
			t.Errorf("%s processed %d times", id, n)
		}
	}
	twice := 0
	for _, n := range src.calls {
		if n == 2 {
			twice++
		}
	}
	if twice > 1 {
		t.Errorf("%d images processed twice, at most the interrupted one may be", twice)
	}
	if len(src.calls) != 30 {
		t.Errorf("%d images seen", len(src.calls))
	}
	// Resume with nothing saved does nothing.
	r2.Resume(ctx2)
	r2.Wait(ctx2)
	if log.done != 1 {
		t.Errorf("done hook ran %d times", log.done)
	}
}

func TestRebuildRecordsSkippedImagesAndContinues(t *testing.T) {
	ctx := context.Background()
	src, store := newFakeSource(30), &memState{}
	for _, id := range src.ids[:25] {
		src.fail[id] = errors.New("decode failed")
		src.nameByID[id] = "broken.jpg"
	}
	r := NewRebuilder(src, store, func() context.Context { return ctx }, RebuildHooks{}, quiet())
	if err := r.Start(ctx, "o"); err != nil {
		t.Fatal(err)
	}
	r.Wait(ctx)
	st, _ := r.Status(ctx)
	if st.Done != 30 || st.SkippedCount != 25 || len(st.Skipped) != maxSkipped || st.Last.Skipped != 25 {
		t.Fatalf("state %+v", st)
	}
	if st.Skipped[0].Reason != "decode failed" || st.Skipped[0].Name != "broken.jpg" {
		t.Errorf("skipped %+v", st.Skipped[0])
	}
}
