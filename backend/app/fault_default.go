//go:build !hatamoto_testkeys

package app

// faultAfterMigrate is a no-op: fault injection exists only in test builds.
func faultAfterMigrate() {}
