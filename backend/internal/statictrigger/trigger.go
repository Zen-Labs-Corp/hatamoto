// Package statictrigger turns public-output changes into debounced static
// exports: one run at a time, none while the site is served.
package statictrigger

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Window is the trailing debounce window: an export starts this long after
// the last change. Tests override the field, never the constant.
const Window = 30 * time.Second

// capWindows bounds how long constant edits can postpone an export: a run
// starts at most capWindows*window after the first change of a burst.
const capWindows = 5

// Timer is the part of *time.Timer the trigger needs.
type Timer interface{ Stop() bool }

// Run performs one export. full re-publishes every file; reason names what
// changed.
type Run func(ctx context.Context, full bool, reason string) error

// ErrQueued is returned by Now when an export is running: another starts when
// it finishes.
var ErrQueued = errors.New("an export is running; another starts when it finishes")

// ErrRetry is returned (wrapped) by a Run that could not start because another
// process holds the run. The trigger keeps the pending change and re-arms one
// window; it is not logged as a failure.
var ErrRetry = errors.New("export busy elsewhere, retry")

// Trigger debounces change notifications into export runs. A nil *Trigger is
// valid: every method does nothing.
type Trigger struct {
	base   func() context.Context // lifecycle context, read when a run starts
	run    Run
	active func(context.Context) (bool, error) // reads the saved mode: true = static
	log    *slog.Logger

	// Test seams.
	window    time.Duration
	afterFunc func(time.Duration, func()) Timer
	now       func() time.Time

	mu       sync.Mutex
	wg       sync.WaitGroup // runs in flight, for tests
	timer    Timer
	gen      uint64
	first    time.Time // first change of the open window
	dirty    bool      // a change waits for a run
	full     bool
	reasons  []string
	running  bool
	again    bool // the window closed during a run: run once more when it ends
	closed   bool
	activeAt time.Time // when the cached mode was read
	activeOK bool      // the cache holds a value
	activeV  bool
}

// New returns a trigger. base gives the context runs execute under (it may
// return nil before the server started); active reports whether static
// hosting is on.
func New(base func() context.Context, run Run, active func(context.Context) (bool, error), log *slog.Logger) *Trigger {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Trigger{
		base: base, run: run, active: active, log: log,
		window:    Window,
		afterFunc: func(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) },
		now:       time.Now,
	}
}

func (t *Trigger) ctx() context.Context {
	if t.base != nil {
		if c := t.base(); c != nil {
			return c
		}
	}
	return context.Background()
}

// Notify records a change. While static hosting is off it does nothing: no
// timer, no run. Otherwise a run starts one window after the last change
// (full when any change in the window asked for it).
func (t *Trigger) Notify(reason string, full bool) {
	if t == nil {
		return
	}
	on, ok := t.cachedActive()
	if !ok || !on {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.addLocked(reason, full)
	t.armLocked()
}

// Now starts a run without waiting for the window, for a change the owner just
// made on purpose. The caller has just saved the mode, so the cached mode is
// not consulted. While a run is in progress it returns ErrQueued and one more
// run follows.
func (t *Trigger) Now(reason string, full bool) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.addLocked(reason, full)
	if t.running {
		t.again = true
		return ErrQueued
	}
	t.startLocked()
	return nil
}

// Refresh drops the cached mode; call it after the mode is saved.
func (t *Trigger) Refresh() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.activeOK = false
	t.mu.Unlock()
}

// Close stops the pending window. A run in progress is not interrupted, and
// no further run starts.
func (t *Trigger) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.gen++
	t.stopLocked()
}

// wait blocks until no run is in flight (tests).
func (t *Trigger) wait() { t.wg.Wait() }

// cachedActive answers the saved mode from a cache valid for one window. A
// failed read counts as "do not export".
func (t *Trigger) cachedActive() (on, ok bool) {
	t.mu.Lock()
	if t.activeOK && t.now().Sub(t.activeAt) < t.window {
		on = t.activeV
		t.mu.Unlock()
		return on, true
	}
	t.mu.Unlock()

	v, err := t.active(t.ctx())
	if err != nil {
		t.log.Debug("static export: reading the hosting mode failed", "err", err)
		return false, false
	}
	t.mu.Lock()
	t.activeV, t.activeAt, t.activeOK = v, t.now(), true
	t.mu.Unlock()
	return v, true
}

func (t *Trigger) addLocked(reason string, full bool) {
	t.dirty = true
	t.full = t.full || full
	for _, r := range t.reasons {
		if r == reason {
			return
		}
	}
	t.reasons = append(t.reasons, reason)
}

func (t *Trigger) stopLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// armLocked (re)starts the window, shortened so the burst's first change is
// exported within capWindows windows.
func (t *Trigger) armLocked() {
	now := t.now()
	if t.first.IsZero() {
		t.first = now
	}
	delay := min(t.window, t.first.Add(capWindows*t.window).Sub(now))
	t.armAfterLocked(max(delay, 0))
}

func (t *Trigger) armAfterLocked(d time.Duration) {
	t.stopLocked()
	t.gen++
	gen := t.gen
	t.timer = t.afterFunc(d, func() { t.fire(gen) })
}

// fire runs when a window closes. gen identifies the timer: a superseded one
// does nothing.
func (t *Trigger) fire(gen uint64) {
	t.mu.Lock()
	if gen != t.gen || t.closed {
		t.mu.Unlock()
		return
	}
	t.timer = nil
	t.mu.Unlock()

	// Re-read the mode: it may have flipped to served since the notification.
	on, err := t.active(t.ctx())

	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen || t.closed {
		return
	}
	if err != nil {
		t.log.Warn("static export: reading the hosting mode failed, will retry", "err", err)
		t.armAfterLocked(t.window)
		return
	}
	t.activeV, t.activeAt, t.activeOK = on, t.now(), true
	if !on {
		t.dropLocked()
		return
	}
	if t.running {
		t.again = true
		return
	}
	t.startLocked()
}

func (t *Trigger) dropLocked() {
	t.dirty, t.full, t.reasons, t.first, t.again = false, false, nil, time.Time{}, false
}

// startLocked starts a run for the pending changes. mu must be held and no run
// in progress.
func (t *Trigger) startLocked() {
	full, reason := t.full, strings.Join(t.reasons, ",")
	t.stopLocked()
	t.gen++
	t.dropLocked()
	t.running = true
	ctx := context.WithoutCancel(t.ctx())
	t.wg.Add(1)
	go func() {
		err := t.run(ctx, full, reason)
		t.finish(err, full, reason)
		t.wg.Done()
	}()
}

func (t *Trigger) finish(err error, full bool, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = false
	retry := errors.Is(err, ErrRetry)
	switch {
	case retry:
		// Keep what this run was for; it merges with anything newer.
		for _, r := range strings.Split(reason, ",") {
			t.addLocked(r, full)
		}
	case err != nil:
		t.log.Warn("static export failed", "reason", reason, "err", err)
	}
	if t.closed {
		return
	}
	if retry {
		t.again = false
		t.armAfterLocked(t.window)
		return
	}
	if t.again && t.dirty {
		t.again = false
		t.startLocked()
	}
}
