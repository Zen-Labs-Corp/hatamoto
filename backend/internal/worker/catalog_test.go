package worker

import (
	"slices"
	"testing"
)

func TestCatalogIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Catalog {
		if d.ID == "" || d.Name == "" || d.Schedule == "" {
			t.Errorf("incomplete entry %+v", d)
		}
		if seen[d.ID] {
			t.Errorf("duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if IsPluginJob(d.ID) {
			t.Errorf("%q: plugin ids are not catalogue jobs", d.ID)
		}
	}
}

func TestCatalogSafeSet(t *testing.T) {
	var got []string
	for _, d := range Catalog {
		if d.Safe {
			got = append(got, d.ID)
		}
	}
	slices.Sort(got)
	want := []string{"agent_hits_maintenance", "changeset_sweep", "entity_versions", "prune", "publish",
		"retention_purge", "traffic_prune", "update_check", "webhook_prune"}
	if !slices.Equal(got, want) {
		t.Fatalf("safe jobs = %v, want %v (a new job is not safe until decided)", got, want)
	}
}

func TestSwitchJobs(t *testing.T) {
	got := SwitchJobs()
	if len(got) != 17 || slices.Contains(got, "core:preload") || !slices.Contains(got, "backup_offsite") {
		t.Fatalf("SwitchJobs = %v", got)
	}
}
