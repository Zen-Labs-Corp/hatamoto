//go:build hatamoto_testkeys

package installstats

// allowLoopbackHTTP lets the stats URL be plain http on a loopback host. It is
// compiled only with the hatamoto_testkeys tag
// (internal/release/testkeys_guard_test.go).
func allowLoopbackHTTP() bool { return true }
