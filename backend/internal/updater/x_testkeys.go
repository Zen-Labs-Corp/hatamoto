//go:build hatamoto_testkeys

package updater

import (
	"os"
	"time"
)

// Test seams, compiled only with the hatamoto_testkeys tag. They let the
// end-to-end harness compress time and talk to a local server; the default
// build contains none of this (internal/release/testkeys_guard_test.go).

// allowLoopbackHTTP lets the update URL be plain http on a loopback host.
func allowLoopbackHTTP() bool { return true }

// testInterval reads HATAMOTO_TEST_UPDATE_INTERVAL (a Go duration): the check
// interval, with the first check after min(interval, 3s). Jitter is off.
func testInterval() (every, first time.Duration, ok bool) {
	d, err := time.ParseDuration(os.Getenv("HATAMOTO_TEST_UPDATE_INTERVAL"))
	if err != nil || d <= 0 {
		return 0, 0, false
	}
	return d, min(d, 3*time.Second), true
}

// clockOffset reads HATAMOTO_TEST_CLOCK_OFFSET: a duration added to the clock
// the updater uses, so ramp dates can open without waiting. Forward only;
// negative or unparsable values are ignored.
func clockOffset() time.Duration {
	d, err := time.ParseDuration(os.Getenv("HATAMOTO_TEST_CLOCK_OFFSET"))
	if err != nil || d < 0 {
		return 0
	}
	return d
}
