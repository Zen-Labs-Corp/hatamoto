package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/statictrigger"
)

// triggerClock never fires a timer by itself: the test does.
type triggerClock struct{ timers []func() }

type noTimer struct{}

func (noTimer) Stop() bool { return true }

func (c *triggerClock) afterFunc(_ time.Duration, f func()) statictrigger.Timer {
	c.timers = append(c.timers, f)
	return noTimer{}
}

type triggerRun struct {
	full   bool
	reason string
}

// newTriggerServer returns a Server whose trigger is on (static mode) and
// whose runs land on the returned channel once the test fires the window.
func newTriggerServer(t *testing.T) (*Server, *triggerClock, chan triggerRun) {
	t.Helper()
	runs := make(chan triggerRun, 4)
	clock := &triggerClock{}
	tr := statictrigger.New(nil, func(_ context.Context, full bool, reason string) error {
		runs <- triggerRun{full, reason}
		return nil
	}, func(context.Context) (bool, error) { return true, nil }, quietLogger())
	tr.SetClock(clock.afterFunc, time.Now)
	t.Cleanup(tr.Close)
	return &Server{logger: quietLogger(), staticTrigger: tr}, clock, runs
}

// fire closes the open window and returns the run it started.
func (c *triggerClock) fire(t *testing.T, runs chan triggerRun) triggerRun {
	t.Helper()
	if len(c.timers) == 0 {
		t.Fatal("no notification armed a window")
	}
	c.timers[len(c.timers)-1]()
	return <-runs
}

func TestStaticTriggerHooks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(s *Server)
		full bool
		want string
	}{
		{"invalidateRendered", func(s *Server) { s.invalidateRendered(ctx) }, false, "change"},
		{"invalidateEntityRendered", func(s *Server) { s.invalidateEntityRendered(ctx, uuid.New()) }, false, "entity"},
		{"dropRenderedEntity", func(s *Server) { s.dropRenderedEntity(ctx, uuid.New()) }, false, "comment"},
		{"redirects", func(s *Server) { s.notifyRedirectsChanged() }, false, "redirects"},
		{"media", func(s *Server) { s.onMediaChange(ctx) }, false, "change"},
		{"site settings", func(s *Server) { s.invalidateSiteSettings(ctx) }, true, "change,settings"},
		{"theme", func(s *Server) { s.onThemeChange(ctx) }, true, "change,theme"},
		{"scheduled publish", func(s *Server) {
			scheduledPublishSink{s: s, next: noopPublished{}}.EntityPublished(ctx, uuid.New(), "page", "p", "P", 1)
		}, false, "change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, clock, runs := newTriggerServer(t)
			s.packagesBooted.Store(true)
			tc.call(s)
			if got := clock.fire(t, runs); got.full != tc.full || got.reason != tc.want {
				t.Errorf("run = %+v, want full=%v reason=%q", got, tc.full, tc.want)
			}
		})
	}
}

type noopPublished struct{}

func (noopPublished) EntityPublished(context.Context, uuid.UUID, string, string, string, int) {}

func TestStaticTriggerHooksWithoutTrigger(t *testing.T) {
	s := &Server{logger: quietLogger()}
	ctx := context.Background()
	s.invalidateRendered(ctx)
	s.invalidateEntityRendered(ctx, uuid.New())
	s.dropRenderedEntity(ctx, uuid.New())
	s.notifyRedirectsChanged()
	s.onThemeChange(ctx)
	scheduledPublishSink{s: s, next: noopPublished{}}.EntityPublished(ctx, uuid.New(), "page", "p", "P", 1)
}

// The boot-time package reconcile reports the installed theme as a change. That
// must not arm an export, or every restart of a static site rebuilds it all.
func TestThemeChangeAtBootDoesNotArmExport(t *testing.T) {
	s, clock, runs := newTriggerServer(t)
	s.onThemeChange(context.Background()) // boot reconcile
	if len(clock.timers) != 0 {
		t.Fatal("the boot reconcile armed an export window")
	}
	s.packagesBooted.Store(true)
	s.onThemeChange(context.Background()) // a real change after boot
	if got := clock.fire(t, runs); !got.full || got.reason != "change,theme" {
		t.Errorf("run after boot = %+v, want a full theme run", got)
	}
}
