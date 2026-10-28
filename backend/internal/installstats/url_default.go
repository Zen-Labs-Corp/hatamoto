//go:build !hatamoto_testkeys

package installstats

// The default build sends only over https.
func allowLoopbackHTTP() bool { return false }
