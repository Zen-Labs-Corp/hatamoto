package job_test

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/module/job"
)

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return
}

func TestRegister(t *testing.T) {
	t.Cleanup(job.ResetForTest)
	run := func(context.Context) error { return nil }
	job.Register("example", "daily", 24*time.Hour, run)
	jobs := job.JobsForTest()
	if len(jobs) != 1 || jobs[0].ID() != "module:example:daily" || jobs[0].Every != 24*time.Hour {
		t.Fatalf("jobs: %+v", jobs)
	}
	if !panics(func() { job.Register("example", "daily", time.Hour, run) }) {
		t.Error("duplicate did not panic")
	}
	if !panics(func() { job.Register("example", "fast", 30*time.Second, run) }) {
		t.Error("30 s did not panic")
	}
	if !panics(func() { job.Register("example", "nil", time.Hour, nil) }) {
		t.Error("nil run did not panic")
	}
	job.FreezeForTest()
	if !panics(func() { job.Register("example", "late", time.Hour, run) }) {
		t.Error("register after freeze did not panic")
	}
}
