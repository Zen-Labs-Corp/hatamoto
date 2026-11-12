package cache

import "math"

const (
	autoCapFloor   = 8 << 20  // never below this
	autoCapNoLimit = 64 << 20 // used when the process has no memory limit
)

// AutoMaxBytes is the cache byte cap used when none is configured: a tenth of
// the process memory limit, at least 8 MiB, and 64 MiB without a limit.
func AutoMaxBytes(limit int64) int64 {
	if limit <= 0 || limit == math.MaxInt64 {
		return autoCapNoLimit
	}
	return max(autoCapFloor, limit/10)
}
