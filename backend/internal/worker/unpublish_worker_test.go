package worker

import (
	"testing"
)

// TestSetUnpublishWorkerRegisters locks the setter the publish tick keys off.
func TestSetUnpublishWorkerRegisters(t *testing.T) {
	s := NewScheduler(nil, quietLogger())
	if s.unpublishWorker != nil {
		t.Fatal("unpublishWorker should be nil before SetUnpublishWorker")
	}
	s.SetUnpublishWorker(NewUnpublishWorker(nil, quietLogger(), nil))
	if s.unpublishWorker == nil {
		t.Fatal("SetUnpublishWorker did not register the worker")
	}
}
