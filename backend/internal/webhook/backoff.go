package webhook

import "time"

// Backoff is the wait before the next attempt after the attempt'th failed one:
// base * 2^attempt, at most MaxBackoff.
func Backoff(attempt int, base time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt && d < MaxBackoff; i++ {
		d *= 2
	}
	return min(d, MaxBackoff)
}
