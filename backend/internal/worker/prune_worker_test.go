package worker

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auditlog"
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

func auditWheres(t *testing.T, w *PruneWorker) (admin, agent string, names []string) {
	t.Helper()
	for _, j := range w.jobs(context.Background()) {
		names = append(names, j.name)
		switch j.name {
		case "admin-audit":
			admin = j.where
		case "mcp-audit":
			agent = j.where
		}
	}
	return admin, agent, names
}

func TestPruneJobs_AuditRetention(t *testing.T) {
	set := func(a, m int, err error) *PruneWorker {
		w := &PruneWorker{}
		w.SetAuditRetention(func(context.Context) (int, int, error) { return a, m, err })
		return w
	}
	const (
		year = "at < NOW() - INTERVAL '1 year'"
		d90  = "at < NOW() - INTERVAL '90 days'"
	)
	cases := []struct {
		name         string
		w            *PruneWorker
		admin, agent string
	}{
		{"unset", &PruneWorker{}, year, d90},
		{"defaults", set(365, 90, nil), year, d90},
		{"raised", set(730, 90, nil), "at < NOW() - INTERVAL '2 years'", d90},
		{"agent raised", set(365, 180, nil), year, "at < NOW() - INTERVAL '180 days'"},
		{"unknown", set(7, 0, nil), year, d90},
	}
	for _, c := range cases {
		admin, agent, _ := auditWheres(t, c.w)
		if admin != c.admin || agent != c.agent {
			t.Errorf("%s: admin %q agent %q, want %q %q", c.name, admin, agent, c.admin, c.agent)
		}
	}
}

func TestPruneJobs_AuditRetentionErrorSkipsAuditJobs(t *testing.T) {
	w := &PruneWorker{}
	w.SetAuditRetention(func(context.Context) (int, int, error) { return 0, 0, errors.New("boom") })
	_, _, names := auditWheres(t, w)
	if !slices.Equal(names, []string{"404-log", "admin-recovery"}) {
		t.Errorf("jobs after a failed read = %v", names)
	}
}

func TestPruneJobs_AuditRetentionDoesNotMutateJobs(t *testing.T) {
	w := &PruneWorker{}
	w.SetAuditRetention(func(context.Context) (int, int, error) { return 1095, 1095, nil })
	w.jobs(context.Background())
	for _, j := range retentionJobs {
		if j.name == "admin-audit" && j.where != "at < NOW() - INTERVAL '1 year'" || j.name == "mcp-audit" && j.where != "at < NOW() - INTERVAL '90 days'" {
			t.Errorf("retentionJobs mutated: %s %q", j.name, j.where)
		}
	}
}

func TestAuditRetentionMapsMatchOptions(t *testing.T) {
	for name, m := range map[string]map[int]string{"admin": auditAdminWhere, "agent": auditAgentWhere} {
		if len(m) != len(auditlog.RetentionDays) {
			t.Errorf("%s map has %d keys, want %d", name, len(m), len(auditlog.RetentionDays))
		}
		for _, d := range auditlog.RetentionDays {
			if m[d] == "" {
				t.Errorf("%s map lacks %d days", name, d)
			}
		}
	}
}
