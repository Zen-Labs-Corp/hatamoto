package audit

import "hatamoto.dev/core/internal/auditstate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = auditstate.Reset
	FreezeForTest = auditstate.Freeze
)
