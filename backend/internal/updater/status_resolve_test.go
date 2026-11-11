package updater

import (
	"strings"
	"testing"
	"time"
)

func TestResolveStatus(t *testing.T) {
	fin := time.Date(2026, 11, 10, 10, 23, 0, 0, time.UTC)
	applying := Status{Decision: KindApplying, Target: "v1.2.1", Reason: "staged v1.2.1; supervisor notified"}
	cases := []struct {
		name     string
		st       Status
		r        SwitchResult
		decision string
		reason   string // substring
	}{
		{"success", applying, SwitchResult{Running: "v1.2.1", Outcome: "success", FinishedAt: fin}, KindUpToDate, "running v1.2.1 (switched 10:23 UTC, gate passed)"},
		{"pending", applying, SwitchResult{Pending: true, Running: "v1.2.1", Outcome: "success", FinishedAt: fin}, KindApplying, "staged v1.2.1"},
		{"no event", applying, SwitchResult{Running: "v1.2.1"}, KindApplying, "staged v1.2.1"},
		{"success but another version runs", applying, SwitchResult{Running: "v1.2.0", Outcome: "success", FinishedAt: fin}, KindApplying, "staged v1.2.1"},
		{"rolled back", applying, SwitchResult{Running: "v1.2.0", Outcome: "rolled_back", Error: "health gate failed", FinishedAt: fin}, KindRolledBack, "health gate failed"},
		{"failed", applying, SwitchResult{Running: "v1.2.0", Outcome: "failed", FinishedAt: fin}, KindRolledBack, "the switch failed"},
		{"other decision", Status{Decision: KindUpToDate, Target: "v1.2.1", Reason: "x"}, SwitchResult{Running: "v1.2.1", Outcome: "success"}, KindUpToDate, "x"},
		{"no target", Status{Decision: KindApplying}, SwitchResult{Outcome: "success"}, KindApplying, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveStatus(c.st, c.r)
			if got.Decision != c.decision || !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("got %q / %q, want %q / %q", got.Decision, got.Reason, c.decision, c.reason)
			}
		})
	}
}
