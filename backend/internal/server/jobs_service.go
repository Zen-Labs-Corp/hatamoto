package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/worker"
)

// jobsService implements admin.JobsService over the job catalogue, the last
// runs stored in the database and this process's scheduler (nil when the
// worker is off).
type jobsService struct {
	s    *Server
	last func(context.Context) (map[string]worker.Run, error) // nil: the status table
}

func (j jobsService) Snapshot(ctx context.Context) (admin.JobsView, error) {
	s := j.s
	list := j.last
	if list == nil {
		list = worker.NewStatusStore(s.db).List
	}
	last, err := list(ctx)
	if err != nil {
		return admin.JobsView{}, fmt.Errorf("job status: %w", err)
	}
	var jobs []worker.JobInfo
	live := map[string]worker.JobInfo{}
	if s.scheduler != nil {
		jobs = s.scheduler.Jobs()
		for _, ji := range jobs {
			live[ji.ID] = ji
		}
	}
	running := s.scheduler != nil && s.scheduler.Running()
	v := admin.JobsView{WorkerHere: s.scheduler != nil, Drain: worker.DrainMode()}

	row := func(id, name, schedule string) admin.JobRow {
		r := admin.JobRow{ID: id, Name: name, Schedule: schedule}
		if lr, ok := last[id]; ok {
			r.Last, r.Took, r.OK, r.Error = lr.Started, lr.Duration(), lr.OK, lr.Err
		}
		if ji, ok := live[id]; ok {
			r.Next, r.NextApprox, r.Running = ji.Next, ji.Jitter > 0, ji.Running
		}
		return r
	}
	for _, d := range worker.Catalog {
		r := row(d.ID, d.Name, d.Schedule)
		_, registered := live[d.ID]
		r.Disabled = d.Switch && !workerJobEnabled(d.ID)
		r.CanRun = d.Safe && running && registered
		v.Rows = append(v.Rows, r)
	}
	for _, ji := range jobs {
		if worker.IsPluginJob(ji.ID) {
			v.Rows = append(v.Rows, row(ji.ID, pluginJobName(ji.ID), "every "+shortDuration(ji.Every)))
		}
	}
	return v, nil
}

func (j jobsService) RunNow(ctx context.Context, id string) (admin.JobRunResult, error) {
	if j.s.scheduler == nil {
		return admin.JobRunResult{}, worker.ErrNotRunning
	}
	def, _ := worker.Lookup(id)
	// The handler waits a while; a longer job keeps running on its own.
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := j.s.scheduler.RunNow(wait, id)
	if errors.Is(err, worker.ErrStillRunning) {
		return admin.JobRunResult{Name: def.Name, Started: true}, nil
	}
	if err != nil {
		return admin.JobRunResult{}, err
	}
	return admin.JobRunResult{Name: def.Name, OK: r.OK, Took: r.Duration(), Error: r.Err}, nil
}

// pluginJobName turns plugin:<name>:<job> into "Plugin: <name> > <job>".
func pluginJobName(id string) string {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) < 3 {
		return id
	}
	return "Plugin: " + parts[1] + " > " + parts[2]
}

// shortDuration prints d for a schedule: "30 s", "5 min", "2 h".
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}
