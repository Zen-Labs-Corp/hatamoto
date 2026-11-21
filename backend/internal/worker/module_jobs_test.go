package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestModuleTiming(t *testing.T) {
	for _, c := range []struct{ every, poll, jitter, first, window time.Duration }{
		{time.Minute, 6 * time.Second, 3 * time.Second, 6 * time.Second, time.Minute},
		{24 * time.Hour, 5 * time.Minute, 150 * time.Second, 5 * time.Minute, 24 * time.Hour},
	} {
		p, j, f, w := moduleTiming(c.every)
		if p != c.poll || j != c.jitter || f != c.first || w != c.window {
			t.Errorf("moduleTiming(%s) = %s %s %s %s", c.every, p, j, f, w)
		}
	}
}

func TestRecoveredTurnsPanicIntoError(t *testing.T) {
	err := recovered(func(context.Context) error { panic("boom") })(context.Background())
	if err == nil || err.Error() != "module job panicked: boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestScheduleModuleJobs(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	claim := func(context.Context, string, time.Duration) (bool, error) { return true, nil }
	s := NewScheduler(nil, log)
	if n := ScheduleModuleJobs(s, claim, nil, log); n != 0 || len(s.Jobs()) != 0 {
		t.Fatalf("empty: n=%d jobs=%v", n, s.Jobs())
	}
	job := ModuleJob{ID: "module:a:b", Every: time.Minute, Run: func(context.Context) error { return nil }}
	if n := ScheduleModuleJobs(s, claim, []ModuleJob{job}, log); n != 1 || len(s.Jobs()) != 1 {
		t.Fatalf("one: n=%d jobs=%v", n, s.Jobs())
	}
}

func TestIsModuleJob(t *testing.T) {
	if !IsModuleJob("module:a:b") || IsModuleJob("plugin:a") || IsModuleJob("prune") {
		t.Fatal("IsModuleJob")
	}
}
