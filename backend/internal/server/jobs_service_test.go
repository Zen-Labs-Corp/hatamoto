package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/worker"
)

func snapshotRows(t *testing.T, s *Server, last map[string]worker.Run) map[string]admin.JobRow {
	t.Helper()
	v, err := jobsService{s: s, last: func(context.Context) (map[string]worker.Run, error) { return last, nil }}.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]admin.JobRow{}
	for _, r := range v.Rows {
		rows[r.ID] = r
	}
	if len(rows) != len(v.Rows) {
		t.Fatalf("duplicate row ids in %+v", v.Rows)
	}
	return rows
}

func TestJobsSnapshotHasARowForEveryKnownJob(t *testing.T) {
	rows := snapshotRows(t, &Server{}, nil) // worker off
	for _, id := range knownWorkerJobs {
		if _, ok := rows[id]; !ok {
			t.Errorf("no row for %q", id)
		}
	}
	if len(rows) != len(worker.Catalog) {
		t.Errorf("%d rows, catalogue has %d", len(rows), len(worker.Catalog))
	}
	for id, r := range rows {
		if r.CanRun {
			t.Errorf("%s: CanRun without a worker", id)
		}
		if !r.Last.IsZero() {
			t.Errorf("%s: last run without data", id)
		}
	}
}

func TestJobsSnapshotMergesLastRunLiveStateAndSwitches(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "prune")
	t.Setenv("HATAMOTO_AGENT_RANGES", "off")
	t.Setenv("WORKER_DRAIN", "")
	sch := worker.NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	noop := func(context.Context) error { return nil }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(sch.Schedule("changeset_sweep", time.Hour, noop))
	must(sch.ScheduleJitter("stats_ping", 24*time.Hour, time.Hour, time.Minute, noop))
	must(sch.Schedule("plugin:seo:rebuild", 5*time.Minute, noop))
	sch.Start(t.Context())
	t.Cleanup(sch.Stop)

	at := time.Now().Add(-time.Hour)
	rows := snapshotRows(t, &Server{scheduler: sch}, map[string]worker.Run{
		"changeset_sweep": {ID: "changeset_sweep", Started: at, Finished: at.Add(2 * time.Second), OK: false, Err: "boom"},
	})

	sweep := rows["changeset_sweep"]
	if !sweep.CanRun || sweep.OK || sweep.Error != "boom" || sweep.Took != 2*time.Second || !sweep.Last.Equal(at) || sweep.Next.IsZero() {
		t.Errorf("sweep = %+v", sweep)
	}
	if st := rows["stats_ping"]; st.CanRun || !st.NextApprox {
		t.Errorf("stats_ping = %+v (outbound job: no Run now; jittered: approximate)", st)
	}
	if p := rows["prune"]; !p.Disabled || p.CanRun {
		t.Errorf("prune = %+v, want disabled", p)
	}
	if r := rows["agent_ranges_refresh"]; !r.Disabled {
		t.Errorf("agent_ranges_refresh = %+v, want disabled", r)
	}
	if rows["publish"].Disabled || rows["core:preload"].Disabled {
		t.Error("only switched-off jobs read as disabled")
	}
	pl, ok := rows["plugin:seo:rebuild"]
	if !ok || pl.Name != "Plugin: seo > rebuild" || pl.CanRun || pl.Schedule != "every 5 min" {
		t.Errorf("plugin row = %+v, %v", pl, ok)
	}
}

func TestJobsRunNowWithoutWorker(t *testing.T) {
	if _, err := (jobsService{s: &Server{}}).RunNow(t.Context(), "prune"); err != worker.ErrNotRunning {
		t.Fatalf("err = %v", err)
	}
}
