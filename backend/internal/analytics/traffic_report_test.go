package analytics

import (
	"context"
	"testing"
	"time"
)

// An unknown dimension is rejected before any connection is taken, so a
// caller-supplied value can never reach the SQL.
func TestTrafficReaderRejectsUnknownDim(t *testing.T) {
	r := &Reader{}
	day := time.Now()
	if _, err := r.TrafficDaily(context.Background(), Dim("x'; --"), day, day); err == nil {
		t.Error("TrafficDaily accepted an unknown dimension")
	}
	if _, err := r.TrafficTop(context.Background(), Dim(""), day, day, 5); err == nil {
		t.Error("TrafficTop accepted an unknown dimension")
	}
}
