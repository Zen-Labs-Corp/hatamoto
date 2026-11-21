package job

import (
	"context"
	"time"

	"hatamoto.dev/core/internal/modhost"
)

// MinEvery is the shortest interval Register accepts.
const MinEvery = time.Minute

// Register adds a job for module that runs run about once per every across
// all serve processes sharing the database. name identifies the job within
// the module; the Jobs screen shows it as module:<module>:<name>. Call it
// from an init function: it panics after app.Main has started, on an invalid
// or duplicate module or job name, on an interval under MinEvery and on a
// nil run.
func Register(module, name string, every time.Duration, run func(ctx context.Context) error) {
	modhost.RegisterJob(modhost.Job{Module: module, Name: name, Every: every, Run: run})
}
