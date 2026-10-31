package analytics

import (
	"context"
	"testing"
)

// SettingsStore is a thin SQL wrapper; the compile-time check pins the shape
// the admin wiring relies on, the round trip is in settings_integration_test.go.
func TestSettingsStoreShape(t *testing.T) {
	var _ interface {
		EngagementEnabled(ctx context.Context) (bool, error)
		SetEngagement(ctx context.Context, on bool) error
	} = (*SettingsStore)(nil)
}
