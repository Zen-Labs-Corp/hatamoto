package worker

import (
	"context"
	"log/slog"
	"testing"
)

func TestRetentionPurgeWorker_NilSessionsNoError(t *testing.T) {
	w := NewRetentionPurgeWorker(nil, slog.Default())
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run with nil sessions should be a no-op, got %v", err)
	}
}
