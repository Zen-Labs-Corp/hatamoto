package worker

import "strings"

// JobDef describes one core job for the Jobs screen and the operator switch.
type JobDef struct {
	ID       string // canonical id: the name WORKER_DISABLE_JOBS uses
	Name     string // label for the owner
	Schedule string // plain-language cadence
	// Safe marks a job the owner may run from the admin: it only maintains
	// local data (or checks for updates) and sends nothing out.
	Safe bool
	// ManualOnly marks a job whose scheduled function may do more than Run
	// now may (the update check also installs): Run now needs the variant set
	// with SetManual and refuses without it.
	ManualOnly bool
	// Switch marks an id WORKER_DISABLE_JOBS can turn off.
	Switch bool
}

// Catalog is the one list of core jobs. A job that is not in it is never
// runnable from the admin; a new entry is not Safe until someone decides so.
var Catalog = []JobDef{
	{ID: "publish", Name: "Scheduled publishing", Schedule: "every minute", Safe: true, Switch: true},
	{ID: "retention_purge", Name: "Session and token purge", Schedule: "daily", Safe: true, Switch: true},
	{ID: "agent_hits_partition", Name: "Agent hit partitions", Schedule: "daily", Switch: true},
	{ID: "agent_hits_maintenance", Name: "Agent hit IP truncation", Schedule: "daily", Safe: true, Switch: true},
	{ID: "agent_ranges_refresh", Name: "Agent IP range refresh", Schedule: "daily", Switch: true},
	{ID: "agent_verify", Name: "Agent verification", Schedule: "every 5 minutes", Switch: true},
	{ID: "agent_attribution_backfill", Name: "Agent attribution backfill", Schedule: "daily", Switch: true},
	{ID: "prune", Name: "Table prune", Schedule: "daily", Safe: true, Switch: true},
	{ID: "entity_versions", Name: "Entity version cleanup", Schedule: "daily", Safe: true, Switch: true},
	{ID: "changeset_sweep", Name: "Change set sweep", Schedule: "hourly", Safe: true, Switch: true},
	{ID: "traffic_prune", Name: "Traffic rollup prune", Schedule: "daily", Safe: true, Switch: true},
	{ID: "webhook_deliver", Name: "Webhook delivery", Schedule: "every 10 seconds", Switch: true},
	{ID: "webhook_prune", Name: "Webhook log prune", Schedule: "hourly", Safe: true, Switch: true},
	{ID: "update_check", Name: "Update check", Schedule: "every 6 hours", Safe: true, ManualOnly: true, Switch: true},
	{ID: "standing_digest", Name: "Standing digest mail", Schedule: "daily", Switch: true},
	{ID: "user_access", Name: "Expiring user access", Schedule: "hourly", Switch: true},
	{ID: "stats_ping", Name: "Install statistics", Schedule: "daily", Switch: true},
	{ID: "backup_offsite", Name: "Off-site backup", Schedule: "checked every minute", Switch: true},
	{ID: "core:preload", Name: "Page cache preload", Schedule: "as set in page cache settings"},
}

// SwitchJobs returns the ids WORKER_DISABLE_JOBS knows.
func SwitchJobs() []string {
	var out []string
	for _, d := range Catalog {
		if d.Switch {
			out = append(out, d.ID)
		}
	}
	return out
}

// Lookup returns the catalogue entry for id.
func Lookup(id string) (JobDef, bool) {
	for _, d := range Catalog {
		if d.ID == id {
			return d, true
		}
	}
	return JobDef{}, false
}

// IsPluginJob reports whether id is a plugin cron job (plugin:<name>:<job>).
func IsPluginJob(id string) bool { return strings.HasPrefix(id, "plugin:") }
