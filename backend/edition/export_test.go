package edition

import "hatamoto.dev/core/internal/editionstate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = editionstate.Reset
	FreezeForTest = editionstate.Freeze
)
