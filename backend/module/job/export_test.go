package job

import "hatamoto.dev/core/internal/modhost"

// Core-only hooks for the external test package.
var (
	ResetForTest  = modhost.Reset
	FreezeForTest = modhost.Freeze
	JobsForTest   = modhost.Jobs
)
