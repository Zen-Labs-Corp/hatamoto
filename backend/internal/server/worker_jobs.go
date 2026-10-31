package server

import (
	"os"
	"strings"
)

// workerJobEnabled reports whether a background job may be scheduled when
// the in-process worker. WORKER_DISABLE_JOBS is a comma-separated list of job
// names to skip (publish, retention_purge,
// prune, entity_versions, agent_hits_partition,
// agent_hits_maintenance, agent_ranges_refresh, agent_verify, agent_attribution_backfill, update_check, webhook_deliver, webhook_prune, traffic_prune,
// stats_ping) — e.g. so an operator can adopt an
// existing database without immediately applying new retention deletes.
//
// HATAMOTO_AGENT_RANGES=off also turns off agent_ranges_refresh, the only job
// that downloads third-party IP ranges.
func workerJobEnabled(name string) bool {
	if name == "agent_ranges_refresh" && agentRangesOff() {
		return false
	}
	for _, j := range strings.Split(os.Getenv("WORKER_DISABLE_JOBS"), ",") {
		if strings.EqualFold(strings.TrimSpace(j), name) {
			return false
		}
	}
	return true
}

// trafficPruneJob is the nightly retention and top-N compaction of the traffic
// rollup (analytics.PostgresRepository.PruneTraffic).
const trafficPruneJob = "traffic_prune"

// knownWorkerJobs are the names workerJobEnabled is called with.
var knownWorkerJobs = []string{"publish", "retention_purge", "prune", "entity_versions", "agent_hits_partition", "agent_hits_maintenance", "agent_ranges_refresh", "agent_verify", attributionBackfillJob, updateJob, webhookDeliverJob, webhookPruneJob, trafficPruneJob, statsJob}

// disabledWorkerJobs lists the known jobs WORKER_DISABLE_JOBS turns off, for a
// startup log line so operators can see the effect.
func disabledWorkerJobs() []string {
	var out []string
	for _, j := range knownWorkerJobs {
		if !workerJobEnabled(j) {
			out = append(out, j)
		}
	}
	return out
}

// agentRangesOff reports HATAMOTO_AGENT_RANGES=off: no range refresher is
// scheduled and nothing is fetched from the operators' sites.
func agentRangesOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HATAMOTO_AGENT_RANGES")), "off")
}
