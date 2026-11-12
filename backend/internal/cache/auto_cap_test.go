package cache

import (
	"math"
	"testing"
)

func TestAutoMaxBytes(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		limit, want int64
	}{
		{0, 64 * mib},
		{-1, 64 * mib},
		{math.MaxInt64, 64 * mib},
		{64 * mib, 8 * mib},
		{512 * mib, 512 * mib / 10},
		{4096 * mib, 4096 * mib / 10},
	} {
		if got := AutoMaxBytes(tc.limit); got != tc.want {
			t.Errorf("AutoMaxBytes(%d) = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestNewStartsWithAutoCap(t *testing.T) {
	c := New(nil)
	if c.RenderMaxBytes() <= 0 {
		t.Fatalf("New cap = %d, want the automatic cap", c.RenderMaxBytes())
	}
}
