package modhost

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// MinJobEvery is the shortest interval a module job may ask for.
const MinJobEvery = time.Minute

// Job is a module's recurring background function, run by `serve` about once
// per Every across all servers sharing the database.
type Job struct {
	Module string
	Name   string
	Every  time.Duration
	Run    func(ctx context.Context) error
}

var (
	jobs    []Job
	jobName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,40}$`)
)

func init() { onReset(func() { jobs = nil }) }

// ID is the job's scheduler and Jobs screen id.
func (j Job) ID() string { return "module:" + j.Module + ":" + j.Name }

// RegisterJob adds a job. It panics after Freeze, on an invalid module or job
// name, an interval under MinJobEvery, a nil Run and a duplicate
// (module, name) pair.
func RegisterJob(j Job) {
	register("job: Register", func() {
		switch {
		case !ValidName(j.Module):
			panic(fmt.Sprintf("job: invalid module name %q", j.Module))
		case !jobName.MatchString(j.Name):
			panic(fmt.Sprintf("job: module %q has an invalid job name %q", j.Module, j.Name))
		case j.Every < MinJobEvery:
			panic(fmt.Sprintf("job: %s runs every %s, the minimum is %s", j.ID(), j.Every, MinJobEvery))
		case isNil(j.Run):
			panic(fmt.Sprintf("job: %s has a nil run function", j.ID()))
		}
		for _, have := range jobs {
			if have.Module == j.Module && have.Name == j.Name {
				panic(fmt.Sprintf("job: %s registered twice", j.ID()))
			}
		}
		jobs = append(jobs, j)
	})
}

// Jobs returns the registered jobs in registration order.
func Jobs() []Job {
	mu.Lock()
	defer mu.Unlock()
	return append([]Job(nil), jobs...)
}
