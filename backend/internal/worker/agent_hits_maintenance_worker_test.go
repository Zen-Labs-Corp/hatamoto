package worker

import (
	"context"
	"strings"
	"testing"
)

func TestTruncateSQLShape(t *testing.T) {
	for _, want := range []string{
		"network(set_masklen(ip, CASE family(ip) WHEN 4 THEN 24 ELSE 48 END))::inet",
		"ip_truncated = true",
		"ip_truncated = false",
		"interval '30 days'",
		"(id, occurred_at) IN (",
		"LIMIT $1",
	} {
		if !strings.Contains(truncateAgentHitIPsSQL, want) {
			t.Errorf("sql missing %q", want)
		}
	}
	if strings.Contains(truncateAgentHitIPsSQL, "ctid") {
		t.Error("ctid is not unique across partitions; select by primary key")
	}
}

func TestMaintenanceNoDeps(t *testing.T) {
	var nilWorker *AgentHitsMaintenanceWorker
	if err := nilWorker.Run(context.Background()); err != nil {
		t.Fatalf("nil worker: %v", err)
	}
	if err := NewAgentHitsMaintenanceWorker(nil, 0, nil).Run(context.Background()); err != nil {
		t.Fatalf("nil db: %v", err)
	}
	if w := NewAgentHitsMaintenanceWorker(nil, 0, nil); w.batchSize != 5000 {
		t.Fatalf("default batch = %d", w.batchSize)
	}
}
