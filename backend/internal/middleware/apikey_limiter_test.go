package middleware

import (
	"testing"

	"github.com/google/uuid"
)

func TestAllowClampsNonPositive(t *testing.T) {
	rl := NewAPIKeyRateLimiter()
	defer rl.Stop()

	for _, limit := range []int{0, -3} {
		if !rl.Allow(uuid.New(), limit) {
			t.Fatalf("limit %d: first request must be allowed (clamped to default)", limit)
		}
	}
}
