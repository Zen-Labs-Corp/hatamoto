//go:build !hatamoto_testkeys

package server

import "net/http"

// wrapFault returns h unchanged: fault injection exists only in test builds.
func wrapFault(h http.Handler) http.Handler { return h }
