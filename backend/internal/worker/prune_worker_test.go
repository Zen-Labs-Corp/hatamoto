package worker

import "testing"

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
