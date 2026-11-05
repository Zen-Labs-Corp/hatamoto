package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakeJobs is an in-memory JobsService.
type fakeJobs struct {
	view   JobsView
	result JobRunResult
	err    error
	ran    []string
}

func (f *fakeJobs) Snapshot(context.Context) (JobsView, error) { return f.view, nil }

func (f *fakeJobs) RunNow(_ context.Context, id string) (JobRunResult, error) {
	f.ran = append(f.ran, id)
	return f.result, f.err
}

func newJobsRig(t *testing.T) (*ownerRig, *fakeJobs, *[]AuditEvent) {
	t.Helper()
	g := newOwnerRig(t, true)
	at := time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC)
	f := &fakeJobs{
		view: JobsView{WorkerHere: true, Rows: []JobRow{
			{ID: "prune", Name: "Table prune", Schedule: "daily", Last: at, Took: 1300 * time.Millisecond, OK: true, Next: at.Add(24 * time.Hour), CanRun: true},
			{ID: "changeset_sweep", Name: "Change set sweep", Schedule: "hourly", Last: at, Took: 40 * time.Millisecond, OK: false, Error: "database is locked", Next: at.Add(time.Hour), CanRun: true},
			{ID: "stats_ping", Name: "Install statistics", Schedule: "daily", Next: at.Add(5 * time.Hour), NextApprox: true},
			{ID: "agent_verify", Name: "Agent verification", Schedule: "every 5 minutes", Disabled: true},
			{ID: "plugin:seo:rebuild", Name: "Plugin: seo > rebuild", Schedule: "every 5 min", Last: at, Took: 2 * time.Second, OK: true, Next: at.Add(5 * time.Minute)},
		}},
		result: JobRunResult{Name: "Table prune", OK: true, Took: 100 * time.Millisecond},
	}
	g.a.deps.Jobs = f
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	return g, f, &events
}

func TestJobsPage(t *testing.T) {
	t.Parallel()

	g, _, _ := newJobsRig(t)
	rec := g.do("GET", "/admin/settings/jobs", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("jobs = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-jobs.html", normalizePage(body))
	for _, want := range []string{
		`>Job</th>`, `<th scope="row"`, "This server process runs the worker.",
		"Table prune", "1.3 s", "under 0.1 s", "Failed", "Last error: database is locked",
		"Never run", "Turned off by server settings", "About 4 Nov 2026 08:00", "Plugin: seo &gt; rebuild",
		"have no Run now button",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestJobsPageWorkerOffAndDrain(t *testing.T) {
	t.Parallel()

	g, f, _ := newJobsRig(t)
	f.view.WorkerHere = false
	body := g.do("GET", "/admin/settings/jobs", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "does not run the worker") || !strings.Contains(body, "Table prune") || !strings.Contains(body, "1.3 s") {
		t.Error("with the worker off the page must say so and still show the stored last runs")
	}
	f.view.WorkerHere, f.view.Drain = true, true
	if body := g.do("GET", "/admin/settings/jobs", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "drain mode") {
		t.Error("drain mode is not announced")
	}
}

func TestJobsOwnerOnlyAndUnavailable(t *testing.T) {
	t.Parallel()

	g, _, _ := newJobsRig(t)
	if rec := g.do("GET", "/admin/settings/jobs", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `/admin/settings/jobs`) {
		t.Error("the owner has no Jobs tab")
	}
	if body := g.do("GET", "/admin/", "editor-token", nil, nil).Body.String(); strings.Contains(body, `/admin/settings/jobs`) {
		t.Error("the editor sees the Jobs tab")
	}
	g.a.deps.Jobs = nil
	if rec := g.do("GET", "/admin/settings/jobs", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}
