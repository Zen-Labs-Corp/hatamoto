package server

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/worker"
)

func registerExampleJob(t *testing.T) {
	t.Helper()
	modhost.Reset()
	t.Cleanup(modhost.Reset)
	modhost.RegisterJob(modhost.Job{
		Module: "example", Name: "daily", Every: 24 * time.Hour,
		Run: func(context.Context) error { return nil },
	})
}

func TestWireModuleJobsWithoutJobsOrScheduler(t *testing.T) {
	modhost.Reset()
	t.Cleanup(modhost.Reset)
	s := &Server{logger: quietLogger()}
	if n := s.wireModuleJobs(); n != 0 {
		t.Errorf("no jobs registered: scheduled %d", n)
	}
	registerExampleJob(t)
	if n := s.wireModuleJobs(); n != 0 {
		t.Errorf("nil scheduler: scheduled %d", n)
	}
}

func TestWireModuleJobsSchedulesTheRegisteredJob(t *testing.T) {
	registerExampleJob(t)
	s := &Server{logger: quietLogger(), db: &database.DB{}}
	s.scheduler = worker.NewScheduler(nil, s.logger)
	if n := s.wireModuleJobs(); n != 1 {
		t.Fatalf("scheduled %d, want 1", n)
	}
	var found bool
	for _, ji := range s.scheduler.Jobs() {
		found = found || ji.ID == "module:example:daily"
	}
	if !found {
		t.Errorf("module:example:daily not in %v", s.scheduler.Jobs())
	}
}

func TestWireModuleJobsHonoursWorkerDisableJobs(t *testing.T) {
	registerExampleJob(t)
	t.Setenv("WORKER_DISABLE_JOBS", "module:example:daily")
	s := &Server{logger: quietLogger(), db: &database.DB{}}
	s.scheduler = worker.NewScheduler(nil, s.logger)
	if n := s.wireModuleJobs(); n != 0 {
		t.Errorf("disabled job scheduled: %d", n)
	}
}

func TestModuleJobName(t *testing.T) {
	for id, want := range map[string]string{
		"module:example:daily": "Module: example > daily",
		"module:odd":           "module:odd",
	} {
		if got := moduleJobName(id); got != want {
			t.Errorf("moduleJobName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestModuleJobEvery(t *testing.T) {
	registerExampleJob(t)
	if got := moduleJobEvery("module:example:daily"); got != 24*time.Hour {
		t.Errorf("every = %v", got)
	}
	if got := moduleJobEvery("module:example:none"); got != 0 {
		t.Errorf("unknown job every = %v", got)
	}
}

func TestModuleMailSenderIsUntypedNilWithoutService(t *testing.T) {
	s := &Server{logger: quietLogger()}
	if got := s.moduleMailSender(); got != nil {
		t.Errorf("sender = %#v, want untyped nil", got)
	}
}
