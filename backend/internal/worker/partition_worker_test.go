package worker

import (
	"strings"
	"testing"
	"time"
)

func TestFirstOfMonthUTC(t *testing.T) {
	got := firstOfMonthUTC(time.Date(2027, 3, 22, 13, 4, 5, 0, time.UTC))
	want := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestAgentHitsPartitionDDL(t *testing.T) {
	w := NewAgentHitsPartitionWorker(nil, 2, 3, nil)
	name, ddl := w.ddl(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	if name != "hatamoto_agent_hits_2026_10" {
		t.Errorf("name = %q", name)
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS public.hatamoto_agent_hits_2026_10 PARTITION OF public.hatamoto_agent_hits ",
		"FROM ('2026-10-01 00:00:00+00')",
		"TO ('2026-11-01 00:00:00+00')",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("ddl missing %q; got %s", want, ddl)
		}
	}
}

func TestPartitionNamesDoNotCross(t *testing.T) {
	hits := agentHitsPartitionName
	for name, want := range map[string]bool{
		"hatamoto_agent_hits_2026_10":  true,
		"hatamoto_agent_hits_default":  false,
		"hatamoto_other_2026_10":       false,
		"hatamoto_agent_hits_2026_1":   false,
		"hatamoto_agent_hits_2026_10x": false,
	} {
		if got := hits.MatchString(name); got != want {
			t.Errorf("agent hits regex(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestAgentHitsRetentionCutoff(t *testing.T) {
	w := NewAgentHitsPartitionWorker(nil, 2, 3, nil)
	now := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	cutoff := w.dropCutoff(now)
	if want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC); !cutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", cutoff, want)
	}
	for name, want := range map[string]bool{
		"hatamoto_agent_hits_2026_05":   true,
		"hatamoto_agent_hits_2026_06":   true,  // ended 2026-07-01: >= 3 months before 2026-10-01
		"hatamoto_agent_hits_2026_07":   false, // ends 2026-08-01, still inside retention
		"hatamoto_agent_hits_2026_10":   false,
		"hatamoto_agent_hits_default":   false, // never dropped
		"hatamoto_other_2026_01":        false, // other parent's names are never dropped
		"hatamoto_agent_hits_junk_2026": false,
	} {
		if got := w.expired(name, cutoff); got != want {
			t.Errorf("expired(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEnsureStatementsCurrentAndNext(t *testing.T) {
	w := NewAgentHitsPartitionWorker(nil, 2, 3, nil)
	// Year rollover: December must be followed by next January.
	names, ddls := w.ensureStatements(time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), 1)
	if len(names) != 2 || names[0] != "hatamoto_agent_hits_2026_12" || names[1] != "hatamoto_agent_hits_2027_01" {
		t.Fatalf("names = %v", names)
	}
	if !strings.Contains(ddls[1], "FROM ('2027-01-01 00:00:00+00') TO ('2027-02-01 00:00:00+00')") ||
		!strings.Contains(ddls[1], "CREATE TABLE IF NOT EXISTS") {
		t.Errorf("ddl = %s", ddls[1])
	}
	if names, _ := w.ensureStatements(time.Date(2027, 2, 3, 0, 0, 0, 0, time.UTC), 0); len(names) != 1 {
		t.Errorf("ahead=0 should yield only the current month, got %v", names)
	}
}
