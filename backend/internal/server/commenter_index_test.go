package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/pluginkey"
)

type fakeCommenters struct {
	stamps []comment.CommenterStamp
	calls  int
	since  []time.Time
	err    error
	clock  time.Time
}

func (f *fakeCommenters) list(_ context.Context, since time.Time) ([]comment.CommenterStamp, error) {
	f.calls++
	f.since = append(f.since, since)
	if f.err != nil {
		return nil, f.err
	}
	var out []comment.CommenterStamp
	for _, s := range f.stamps {
		if s.CreatedAt.After(since) {
			out = append(out, s)
		}
	}
	return out, nil
}

func newTestIndex(f *fakeCommenters) (*commenterIndex, *pluginkey.Deriver) {
	d := pluginkey.New("test-secret")
	f.clock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	x := newCommenterIndex(f.list, d.Commenter)
	x.now = func() time.Time { return f.clock }
	return x, d
}

func stamp(i int) comment.CommenterStamp {
	return comment.CommenterStamp{ID: uuid.New(), CreatedAt: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)}
}

func TestCommenterIndexResolves(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1), stamp(2)}}
	x, d := newTestIndex(f)
	id, ok, err := x.resolve(context.Background(), "plugin-a", d.Commenter("plugin-a", f.stamps[1].ID))
	if err != nil || !ok || id != f.stamps[1].ID {
		t.Fatalf("resolve = %v %v %v", id, ok, err)
	}
}

func TestCommenterIndexPerPlugin(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}}
	x, d := newTestIndex(f)
	ctx := context.Background()
	id := f.stamps[0].ID
	keyA, keyB := d.Commenter("plugin-a", id), d.Commenter("plugin-b", id)
	if _, ok, _ := x.resolve(ctx, "plugin-a", keyB); ok {
		t.Error("plugin-b's key resolved for plugin-a")
	}
	if _, ok, _ := x.resolve(ctx, "plugin-b", keyA); ok {
		t.Error("plugin-a's key resolved for plugin-b")
	}
	f.clock = f.clock.Add(keyMissGap)
	if got, ok, _ := x.resolve(ctx, "plugin-a", keyA); !ok || got != id {
		t.Error("plugin-a does not resolve its own key")
	}
	if got, ok, _ := x.resolve(ctx, "plugin-b", keyB); !ok || got != id {
		t.Error("plugin-b does not resolve its own key")
	}
}

func TestCommenterIndexDelta(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}}
	x, d := newTestIndex(f)
	ctx := context.Background()
	if _, ok, _ := x.resolve(ctx, "plugin-a", d.Commenter("plugin-a", f.stamps[0].ID)); !ok {
		t.Fatal("first commenter not found")
	}
	late := stamp(30)
	f.stamps = append(f.stamps, late)
	f.clock = f.clock.Add(keyMissGap)
	id, ok, err := x.resolve(ctx, "plugin-a", d.Commenter("plugin-a", late.ID))
	if err != nil || !ok || id != late.ID {
		t.Fatalf("late commenter: %v %v %v", id, ok, err)
	}
	want := f.stamps[0].CreatedAt.Add(-keyScanOverlap)
	if f.calls != 2 || !f.since[1].Equal(want) {
		t.Errorf("calls %d, since %v, want the watermark minus the overlap %v", f.calls, f.since, want)
	}
}

// An importer writes commenters with the date of their oldest imported
// comment, so a row can appear far behind the watermark.
func TestCommenterIndexBackDatedRow(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(30)}}
	x, d := newTestIndex(f)
	ctx := context.Background()
	if _, ok, _ := x.resolve(ctx, "plugin-a", d.Commenter("plugin-a", f.stamps[0].ID)); !ok {
		t.Fatal("first commenter not found")
	}
	old := comment.CommenterStamp{ID: uuid.New(), CreatedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)}
	f.stamps = append(f.stamps, old)
	f.clock = f.clock.Add(keyFullRescanGap)
	if id, ok, err := x.resolve(ctx, "plugin-a", d.Commenter("plugin-a", old.ID)); err != nil || !ok || id != old.ID {
		t.Fatalf("back-dated commenter: %v %v %v", id, ok, err)
	}
}

func TestCommenterIndexMissGuard(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}}
	x, _ := newTestIndex(f)
	ctx := context.Background()
	random := func(i int) string { return fmt.Sprintf("%043d", i) }
	for i := 0; i < 100; i++ {
		if _, ok, err := x.resolve(ctx, "plugin-a", random(i)); ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("100 random keys ran %d scans, want 1", f.calls)
	}
	f.clock = f.clock.Add(keyMissGap)
	x.resolve(ctx, "plugin-a", random(101))
	if f.calls != 2 {
		t.Fatalf("after the gap: %d scans, want 2", f.calls)
	}
}

func TestCommenterIndexBadKeys(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}}
	x, _ := newTestIndex(f)
	ctx := context.Background()
	for _, key := range []string{"", strings.Repeat("a", 42), strings.Repeat("a", 44)} {
		if _, ok, err := x.resolve(ctx, "plugin-a", key); ok || err != nil {
			t.Errorf("key len %d: ok %v err %v", len(key), ok, err)
		}
	}
	if _, ok, err := x.resolve(ctx, "", strings.Repeat("a", 43)); ok || err != nil {
		t.Errorf("empty plugin: ok %v err %v", ok, err)
	}
	var nilIdx *commenterIndex
	if _, ok, err := nilIdx.resolve(ctx, "plugin-a", strings.Repeat("a", 43)); ok || err != nil {
		t.Errorf("nil index: ok %v err %v", ok, err)
	}
	if f.calls != 0 {
		t.Fatalf("list called %d times for bad keys", f.calls)
	}
}

func TestCommenterIndexListError(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}, err: errors.New("down")}
	x, d := newTestIndex(f)
	ctx := context.Background()
	key := d.Commenter("plugin-a", f.stamps[0].ID)
	if _, _, err := x.resolve(ctx, "plugin-a", key); err == nil {
		t.Fatal("want the list error")
	}
	f.err = nil
	if _, ok, err := x.resolve(ctx, "plugin-a", key); ok || err != nil || f.calls != 1 {
		t.Fatalf("retry within the gap: ok %v err %v calls %d, want backoff", ok, err, f.calls)
	}
	f.clock = f.clock.Add(keyMissGap)
	if _, ok, err := x.resolve(ctx, "plugin-a", key); err != nil || !ok {
		t.Fatalf("retry after the gap: ok %v err %v", ok, err)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2", f.calls)
	}
}

// A scan outlives a caller whose deadline is shorter than the scan, so the
// next caller finds the finished index instead of restarting it.
func TestCommenterIndexScanOutlivesCaller(t *testing.T) {
	f := &fakeCommenters{stamps: []comment.CommenterStamp{stamp(1)}}
	x, d := newTestIndex(f)
	release := make(chan struct{})
	x.list = func(ctx context.Context, since time.Time) ([]comment.CommenterStamp, error) {
		<-release
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return f.list(ctx, since)
	}
	key := d.Commenter("plugin-a", f.stamps[0].ID)
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := x.resolve(short, "plugin-a", key); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
	// A waiter whose ctx ends while the scan runs does not block on it.
	waiter, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, _, err := x.resolve(waiter, "plugin-a", key); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter err = %v", err)
	}
	close(release)
	if id, ok, err := x.resolve(context.Background(), "plugin-a", key); err != nil || !ok || id != f.stamps[0].ID {
		t.Fatalf("after the scan: %v %v %v", id, ok, err)
	}
	if f.calls != 1 {
		t.Fatalf("scans = %d, want 1", f.calls)
	}
}
