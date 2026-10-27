package server

import (
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/updater"
	"hatamoto.dev/core/internal/worker"
)

func TestWorkerJobEnabled(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if !workerJobEnabled("prune") {
		t.Fatal("jobs are enabled by default")
	}
	t.Setenv("WORKER_DISABLE_JOBS", " prune , Entity_Versions")
	if workerJobEnabled("prune") || workerJobEnabled("entity_versions") {
		t.Fatal("listed jobs must be disabled (trimmed, case-insensitive)")
	}
	if !workerJobEnabled("publish") {
		t.Fatal("unlisted jobs stay enabled")
	}
}

func TestDisabledWorkerJobs(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "prune,entity_versions,bogus")
	got := disabledWorkerJobs()
	if len(got) != 2 || got[0] != "prune" || got[1] != "entity_versions" {
		t.Fatalf("got %v", got)
	}
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if len(disabledWorkerJobs()) != 0 {
		t.Fatal("none disabled by default")
	}
}

func TestKnownWorkerJobsIncludeAgentHits(t *testing.T) {
	have := map[string]bool{}
	for _, j := range knownWorkerJobs {
		have[j] = true
	}
	for _, want := range []string{"agent_hits_partition", "agent_hits_maintenance", "agent_ranges_refresh", "agent_verify"} {
		if !have[want] {
			t.Errorf("knownWorkerJobs missing %q", want)
		}
	}
}

func TestUpdateCheckJobHonoursDisableList(t *testing.T) {
	found := false
	for _, j := range knownWorkerJobs {
		found = found || j == "update_check"
	}
	if !found {
		t.Fatal("update_check missing from knownWorkerJobs")
	}
	u, err := updater.New(updater.Env{Data: t.TempDir(), Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Setenv("WORKER_DISABLE_JOBS", "Update_Check")
	if scheduleUpdater(worker.NewScheduler(nil, log), u, log) {
		t.Fatal("a disabled job was scheduled")
	}
	if got := disabledWorkerJobs(); len(got) != 1 || got[0] != "update_check" {
		t.Fatalf("disabled: %v", got)
	}
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if !scheduleUpdater(worker.NewScheduler(nil, log), u, log) {
		t.Fatal("an enabled job was not scheduled")
	}
}

func TestAgentRangesSwitch(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	t.Setenv("HATAMOTO_AGENT_RANGES", "")
	if !workerJobEnabled("agent_ranges_refresh") {
		t.Fatal("the range refresh is on by default")
	}
	t.Setenv("HATAMOTO_AGENT_RANGES", " OFF ")
	if workerJobEnabled("agent_ranges_refresh") {
		t.Fatal("HATAMOTO_AGENT_RANGES=off must stop the range refresher being scheduled")
	}
	if !workerJobEnabled("agent_verify") || !workerJobEnabled("prune") {
		t.Fatal("the switch must only affect the range download")
	}
	if got := disabledWorkerJobs(); len(got) != 1 || got[0] != "agent_ranges_refresh" {
		t.Fatalf("disabledWorkerJobs = %v", got)
	}
}
