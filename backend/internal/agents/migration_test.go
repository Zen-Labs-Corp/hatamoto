package agents

import (
	"os"
	"regexp"
	"testing"
)

// Every agent id in the 010 legacy mapping must exist in registry.json, with
// the operator and purpose the registry declares.
func TestBackfillMappingMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/010_agent_daily_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`\('([^']+)', '([^']+)', '([^']+)', '([^']+)'\)`).FindAllStringSubmatch(string(raw), -1)
	if len(rows) < 24 {
		t.Fatalf("parsed %d mapping rows, want at least 24", len(rows))
	}
	reg := Default()
	for _, r := range rows {
		name, id, operator, purpose := r[1], r[2], r[3], r[4]
		if id == "facebookbot" {
			// Superseded: 015 re-keys these rows to facebookexternalhit.
			continue
		}
		var found *Agent
		for i := range reg.Agents {
			if reg.Agents[i].ID == id {
				found = &reg.Agents[i]
			}
		}
		if found == nil {
			t.Errorf("%s: agent id %q not in registry.json", name, id)
			continue
		}
		if found.Operator != operator || string(found.Purpose) != purpose {
			t.Errorf("%s: %q maps to %s/%s, registry has %s/%s", name, id, operator, purpose, found.Operator, found.Purpose)
		}
	}
}
