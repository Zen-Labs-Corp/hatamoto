package purge

import "hatamoto.dev/core/internal/purgestate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = purgestate.Reset
	FreezeForTest = purgestate.Freeze
)
