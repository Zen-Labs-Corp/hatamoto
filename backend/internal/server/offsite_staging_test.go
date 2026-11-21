package server

import (
	"errors"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestOffsiteRunNowIsSuppressedInStaging(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	r := newOffsiteRig(t, at(1, 2, 0))
	if _, err := r.svc.RunNow(t.Context(), "tester"); !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("RunNow error = %v", err)
	}
	r.svc.wg.Wait()
	if r.eng.begun.Load() != 0 || len(r.srv.Objects()) != 0 {
		t.Fatalf("begun %d, objects %d", r.eng.begun.Load(), len(r.srv.Objects()))
	}
}
