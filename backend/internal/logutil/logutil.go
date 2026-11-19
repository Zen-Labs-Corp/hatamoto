// Package logutil holds helpers for writing request-supplied values to logs.
package logutil

import "unicode/utf8"

// MaxField bounds a request-supplied string written to a log record, so one
// record stays far below the line-length limit of the log pipeline.
const MaxField = 1024

// CapField cuts s to MaxField bytes on a rune boundary and marks the cut.
func CapField(s string) string {
	if len(s) <= MaxField {
		return s
	}
	cut := MaxField
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
