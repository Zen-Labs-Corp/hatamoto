package updater

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSkipsUpdaterRun(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.1.0", nil)
	for _, o := range []RunOpts{{CheckOnly: true}, {}} {
		st, err := b.u.Run(context.Background(), o)
		if err != nil {
			t.Fatalf("%s: Run(%+v) = %v", runenv.Updater, o, err)
		}
		if st.LastResult != ResultSkipped || st.Decision != KindOff || st.Reason != "staging mode" {
			t.Fatalf("status = %+v", st)
		}
	}
	if n := len(b.srv.requests()); n != 0 {
		t.Fatalf("receiver saw %d requests", n)
	}
	if len(b.applied)+len(b.staged) != 0 {
		t.Fatal("something was applied")
	}
}
