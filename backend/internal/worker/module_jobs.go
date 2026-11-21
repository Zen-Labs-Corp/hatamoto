package worker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// maxModulePoll caps how often a module job polls for its claim.
const maxModulePoll = 5 * time.Minute

// ModuleJob is a compiled-in module's recurring function.
type ModuleJob struct {
	ID    string
	Every time.Duration
	Run   func(context.Context) error
}

// IsModuleJob reports whether id names a module job.
func IsModuleJob(id string) bool { return strings.HasPrefix(id, "module:") }

// moduleTiming returns the poll period (a tenth of every, at most five
// minutes, at least minJitterInterval), its jitter, the first delay and the
// claim window (every).
func moduleTiming(every time.Duration) (poll, jitter, first, window time.Duration) {
	poll = min(every/10, maxModulePoll)
	poll = max(poll, minJitterInterval)
	return poll, poll / 2, poll, every
}

// ScheduleModuleJobs adds each job to s. The job polls for a claim and runs
// only when it wins, so one run happens per interval across all servers
// sharing the database. It returns how many jobs were scheduled.
func ScheduleModuleJobs(s *Scheduler, claim func(context.Context, string, time.Duration) (bool, error), jobs []ModuleJob, log *slog.Logger) int {
	n := 0
	for _, j := range jobs {
		poll, jitter, first, window := moduleTiming(j.Every)
		run := Once(claim, j.ID, window, recovered(j.Run))
		if err := s.ScheduleJitter(j.ID, poll, jitter, first, run); err != nil {
			log.Error("module job not scheduled", slog.String("job", j.ID), slog.Any("error", err))
			continue
		}
		n++
	}
	return n
}

// recovered turns a panic in fn into an error.
func recovered(fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("module job panicked: %v", r)
			}
		}()
		return fn(ctx)
	}
}
