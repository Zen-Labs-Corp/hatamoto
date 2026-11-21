package installstats

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSuppressesInstallStats(t *testing.T) {
	ctx := context.Background()
	s, _, r := rig(t, nil, "v1.2.3")
	if err := s.Choose(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	t.Setenv(runenv.EnvVar, "staging")
	if err := s.Run(ctx); err != nil {
		t.Fatalf("%s: Run = %v", runenv.InstallStats, err)
	}
	if len(r.reqs) != 0 {
		t.Fatalf("requests = %d, want 0", len(r.reqs))
	}
}
