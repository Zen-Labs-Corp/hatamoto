package worker

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRetentionJobs_Cover404Log(t *testing.T) {
	tables := map[string]bool{}
	for _, j := range retentionJobs {
		tables[j.table] = true
		if j.name == "" || j.where == "" {
			t.Errorf("job %q has empty field", j.name)
		}
	}
	if !tables["hatamoto_404_log"] {
		t.Error("missing prune job for hatamoto_404_log")
	}
}

func TestRetentionJobs_AgeOutRecoveryCopies(t *testing.T) {
	for _, j := range retentionJobs {
		if j.table == "hatamoto_admin_recovery_copies" {
			if j.where != "saved_at < NOW() - INTERVAL '30 days'" {
				t.Errorf("recovery copies pruned by %q, want 30 days of saved_at", j.where)
			}
			return
		}
	}
	t.Error("missing prune job for hatamoto_admin_recovery_copies")
}

func job404(t *testing.T, jobs []pruneJob) pruneJob {
	t.Helper()
	for _, j := range jobs {
		if j.name == "404-log" {
			return j
		}
	}
	t.Fatal("no 404-log job")
	return pruneJob{}
}

func TestPruneJobs_404Retention(t *testing.T) {
	ctx := context.Background()
	retentionOf := func(days int) *PruneWorker {
		w := &PruneWorker{}
		w.SetRetention404(func(context.Context) int { return days })
		return w
	}
	cases := []struct {
		name string
		w    *PruneWorker
		want string
	}{
		{"unset", &PruneWorker{}, "90 days"},
		{"zero", retentionOf(0), "90 days"},
		{"unknown", retentionOf(7), "90 days"},
		{"negative", retentionOf(-30), "90 days"},
		{"30", retentionOf(30), "30 days"},
		{"90", retentionOf(90), "90 days"},
		{"180", retentionOf(180), "180 days"},
		{"365", retentionOf(365), "365 days"},
	}
	for _, c := range cases {
		if got := job404(t, c.w.jobs(ctx)).where; !strings.Contains(got, "'"+c.want+"'") {
			t.Errorf("%s: where %q, want %s", c.name, got, c.want)
		}
	}
	if got := job404(t, retentionJobs).where; !strings.Contains(got, "'90 days'") {
		t.Errorf("shared retentionJobs mutated: %q", got)
	}
}

func TestRetention404Where_FixedLiterals(t *testing.T) {
	re := regexp.MustCompile(`^last_seen_at < NOW\(\) - INTERVAL '(\d+) days'$`)
	if len(retention404Where) != 4 {
		t.Fatalf("want 30/90/180/365, got %d entries", len(retention404Where))
	}
	for days, where := range retention404Where {
		m := re.FindStringSubmatch(where)
		if m == nil || m[1] != strconv.Itoa(days) {
			t.Errorf("%d: %q is not the fixed literal for that key", days, where)
		}
	}
}
