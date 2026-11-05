package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// JobsService is the scheduled-jobs overview behind Settings > Jobs.
type JobsService interface {
	// Snapshot lists every job with its last run and next run.
	Snapshot(ctx context.Context) (JobsView, error)
	// RunNow runs a safe job once. It returns worker.ErrAlreadyRunning,
	// ErrNotSafe, ErrUnknownJob or ErrNotRunning when it refuses.
	RunNow(ctx context.Context, id string) (JobRunResult, error)
}

// JobsView is what the Jobs page shows.
type JobsView struct {
	WorkerHere bool // this process runs the worker
	Drain      bool // WORKER_DRAIN: no job runs
	Rows       []JobRow
}

// JobRow is one job. Last is zero for a job that never ran.
type JobRow struct {
	ID, Name, Schedule string
	Last               time.Time
	Took               time.Duration
	OK                 bool
	Error              string
	Next               time.Time // zero: not known on this process
	NextApprox         bool      // the schedule has jitter
	Running            bool
	Disabled           bool // turned off by server settings
	CanRun             bool
}

// JobRunResult is the outcome of Run now. Started is true when the job
// outlived the wait and is still running.
type JobRunResult struct {
	Name    string
	OK      bool
	Took    time.Duration
	Error   string
	Started bool
}

const jobsPath = "/settings/jobs"

func (a *Admin) mountJobs(owner *httpx.Group) {
	owner.HandleFunc("GET "+jobsPath, a.handleJobs)
}

func (a *Admin) jobsService(w http.ResponseWriter, r *http.Request) (JobsService, bool) {
	if a.deps.Jobs == nil {
		a.unavailable(w, r, "The jobs overview")
		return nil, false
	}
	return a.deps.Jobs, true
}

// jobTook is a run time with a unit: "0.1 s", "12.4 s", "3 min 5 s".
func jobTook(d time.Duration) string {
	switch {
	case d < 100*time.Millisecond:
		return "under 0.1 s"
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s"
	}
	s := int(d.Round(time.Second).Seconds())
	if s < 3600 {
		return strconv.Itoa(s/60) + " min " + strconv.Itoa(s%60) + " s"
	}
	return strconv.Itoa(s/3600) + " h " + strconv.Itoa(s%3600/60) + " min"
}

// jobResult is the Result cell: the text is the label, colour only supplements.
func jobResult(row JobRow) ui.Cell {
	badge := ui.Badge{Label: "OK", Kind: "success"}
	cell := ui.Cell{}
	switch {
	case row.Disabled:
		badge = ui.Badge{Label: "Turned off by server settings", Kind: "muted", Wrap: true}
	case row.Running:
		badge = ui.Badge{Label: "Running now", Kind: "info"}
	case row.Last.IsZero():
		badge = ui.Badge{Label: "Never run", Kind: "muted"}
	case !row.OK:
		badge = ui.Badge{Label: "Failed", Kind: "danger"}
		cell.Sub = "Last error: " + row.Error
	}
	cell.Badge = &badge
	return cell
}

func (a *Admin) jobsTable(r *http.Request, v JobsView) ui.Table {
	t := ui.Table{
		ID: "jobs", Caption: "Scheduled jobs", BaseURL: a.adminPath + jobsPath, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "job", Label: "Job", Role: "title"}, {Key: "schedule", Label: "Schedule"}, {Key: "last", Label: "Last run"},
			{Key: "took", Label: "Took"}, {Key: "result", Label: "Result"}, {Key: "next", Label: "Next run"}},
		Empty: ui.Empty{Icon: "refresh", Title: "No jobs", Body: "Scheduled jobs appear here."},
	}
	for _, j := range v.Rows {
		last, took, next := "Never", "-", "-"
		if !j.Last.IsZero() {
			last, took = fmtDateTime(j.Last.UTC()), jobTook(j.Took)
		}
		if !j.Next.IsZero() && !j.Disabled {
			next = fmtDateTime(j.Next.UTC())
			if j.NextApprox {
				next = "About " + next
			}
		}
		row := ui.Row{ID: j.ID, Label: j.Name, Cells: []ui.Cell{
			{Text: j.Name, Sub: j.ID}, {Text: j.Schedule}, {Text: last, Nowrap: true}, {Text: took, Nowrap: true}, jobResult(j), {Text: next, Nowrap: true},
		}}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func (a *Admin) handleJobs(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.jobsService(w, r)
	if !ok {
		return
	}
	snap, err := svc.Snapshot(r.Context())
	if err != nil {
		a.logger().Error("jobs snapshot failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The jobs could not be loaded.")
		return
	}
	v := a.NewView(w, r, "Jobs")
	v.Section, v.Tab = "jobs", "jobs"
	v.Data = struct {
		Table      ui.Table
		WorkerHere bool
		Drain      bool
	}{a.jobsTable(r, snap), snap.WorkerHere, snap.Drain}
	a.Render(w, r, http.StatusOK, "settings/jobs", v)
}
