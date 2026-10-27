//go:build !hatamoto_testkeys

package updater

import "time"

// The default build has no test seams: updates run on the real interval, the
// real clock, and only over https.

func allowLoopbackHTTP() bool                             { return false }
func testInterval() (every, first time.Duration, ok bool) { return 0, 0, false }
func clockOffset() time.Duration                          { return 0 }
